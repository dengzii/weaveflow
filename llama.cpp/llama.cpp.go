//go:build cgo && windows

package llama_cpp

/*
#cgo windows CFLAGS: -I${SRCDIR}/include -DLLAMA_SHARED
#cgo windows LDFLAGS: -L${SRCDIR}/bin -l:libllama.dll -l:ggml.dll -l:ggml-base.dll -l:ggml-cpu.dll

// Windows here links against the DLLs in bin at build time.
// The built executable still needs those DLLs on the runtime search path.

#include <stdlib.h>
#include "llama.h"

static int32_t falcon_llama_token_to_piece(const struct llama_vocab * vocab, llama_token token, char * buf, int32_t length) {
	return llama_token_to_piece(vocab, token, buf, length, 0, false);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"go.uber.org/zap"
)

var backendOnce sync.Once

var logger *zap.Logger

func init() {
	logger, _ = zap.NewDevelopment()
}

type LoadOptions struct {
	ContextSize       int
	BatchSize         int
	Threads           int
	ThreadsBatch      int
	GPULayers         int
	UseMMap           bool
	UseMLock          bool
	StopOnContextFull bool
}

type GenerateOptions struct {
	MaxTokens   int
	Temperature float32
	TopP        float32
	TopK        int
	Seed        uint32
	AddSpecial  bool
	Stop        []string
}

type GenerateResult struct {
	StopReason string
	TokenCount int
	Content    string
}

type GenerateSummary struct {
	StopReason string
	TokenCount int
	Content    string
}

const (
	StopReasonNone            = ""
	StopReasonEndOfGeneration = "end_of_generation"
	StopReasonStopSequence    = "stop_sequence"
	StopReasonMaxTokens       = "max_tokens"
	StopReasonContextFull     = "context_full"
	StopReasonCancelled       = "cancelled"
)

type Model struct {
	mu            sync.Mutex
	path          string
	model         *C.struct_llama_model
	ctx           *C.struct_llama_context
	vocab         *C.struct_llama_vocab
	nCtx          int
	loadedAt      time.Time
	seed          uint32
	stopOnCtxFull bool
}

func Load(path string, opts LoadOptions) (*Model, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("model path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve model path: %w", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("stat model path: %w", err)
	}

	backendOnce.Do(func() {
		C.llama_backend_init()
	})

	cPath := C.CString(absPath)
	defer C.free(unsafe.Pointer(cPath))

	mparams := C.llama_model_default_params()
	mparams.n_gpu_layers = C.int32_t(opts.GPULayers)
	mparams.use_mmap = C.bool(opts.UseMMap)
	mparams.use_mlock = C.bool(opts.UseMLock)

	logger.Debug("loading model",
		zap.String("path", path),
		zap.Bool("use_mmap", opts.UseMMap),
		zap.Bool("use_mlock", opts.UseMLock),
		zap.Int("gpu_layers", opts.GPULayers),
	)

	model := C.llama_model_load_from_file(cPath, mparams)
	if model == nil {
		return nil, fmt.Errorf("llama.cpp failed to load model: %s", absPath)
	}

	ctxSize := opts.ContextSize
	batchSize := opts.BatchSize
	if batchSize == 0 {
		batchSize = ctxSize
	}

	threads := opts.Threads
	if threads == 0 {
		threads = runtime.NumCPU() - 1
	}

	threadsBatch := opts.ThreadsBatch
	if threadsBatch == 0 {
		threadsBatch = threads
	}

	cparams := C.llama_context_default_params()
	if ctxSize > 0 {
		cparams.n_ctx = C.uint32_t(ctxSize)
	}
	if batchSize > 0 {
		cparams.n_batch = C.uint32_t(batchSize)
	}
	cparams.n_ubatch = cparams.n_batch
	cparams.n_seq_max = 1
	cparams.n_threads = C.int32_t(threads)
	cparams.n_threads_batch = C.int32_t(threadsBatch)
	cparams.embeddings = C.bool(false)
	cparams.offload_kqv = C.bool(true)

	ctx := C.llama_init_from_model(model, cparams)
	if ctx == nil {
		C.llama_model_free(model)
		return nil, errors.New("llama.cpp failed to initialize context")
	}

	m := &Model{
		path:          absPath,
		model:         model,
		ctx:           ctx,
		vocab:         (*C.struct_llama_vocab)(unsafe.Pointer(C.llama_model_get_vocab(model))),
		nCtx:          int(C.llama_n_ctx(ctx)),
		loadedAt:      time.Now(),
		seed:          uint32(time.Now().UnixNano()),
		stopOnCtxFull: opts.StopOnContextFull,
	}

	logger.Debug("model loaded",
		zap.Int("ctx_size", m.nCtx),
		zap.Int("batch_size", batchSize),
		zap.Int("threads", threads),
		zap.Int("threads_batch", threadsBatch),
		zap.Bool("embeddings", false),
		zap.Bool("offload_kqv", true),
	)
	return m, nil
}

func (m *Model) Path() string {
	return m.path
}

func (m *Model) Name() string {
	return strings.TrimSuffix(filepath.Base(m.path), filepath.Ext(m.path))
}

func (m *Model) LoadedAt() time.Time {
	return m.loadedAt
}

func (m *Model) ContextSize() int {
	return m.nCtx
}

func (m *Model) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx != nil {
		C.llama_free(m.ctx)
		m.ctx = nil
	}
	if m.model != nil {
		C.llama_model_free(m.model)
		m.model = nil
	}
	m.vocab = nil
	return nil
}

func (m *Model) Generate(ctx context.Context, prompt string, options GenerateOptions) (<-chan GenerateResult, <-chan error) {
	resultCh := make(chan GenerateResult, 8)
	errCh := make(chan error, 1)

	opts := m.resolveGenerateOption(options)

	logger.Debug("starting generation",
		zap.Int("prompt_length", len(prompt)),
		zap.Int("max_tokens", opts.MaxTokens),
		zap.Float32("temperature", opts.Temperature),
		zap.Float32("top_p", opts.TopP),
		zap.Int("top_k", opts.TopK),
		zap.Strings("stops", opts.Stop),
		zap.Uint32("seed", opts.Seed),
		zap.Bool("add_special", opts.AddSpecial),
		zap.String("prompt", prompt),
	)

	go func() {
		defer close(resultCh)
		defer close(errCh)

		if err := m.generate(ctx, prompt, opts, resultCh); err != nil {
			zap.Error(err)
			errCh <- err
			return
		}
	}()

	return resultCh, errCh
}

func (m *Model) generate(ctx context.Context, prompt string, opts GenerateOptions, resultCh chan<- GenerateResult) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("prompt is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx == nil || m.model == nil || m.vocab == nil {
		return errors.New("model is closed")
	}

	tokens, err := m.tokenize(prompt, opts.AddSpecial)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return errors.New("prompt produced no tokens")
	}
	if len(tokens) >= m.nCtx {
		return fmt.Errorf("prompt token count %d exceeds context size %d", len(tokens), m.nCtx)
	}

	C.llama_memory_clear(C.llama_get_memory(m.ctx), C.bool(true))

	if err := m.decode(tokens); err != nil {
		return err
	}

	sampler := m.newSampler(opts)
	if sampler == nil {
		return errors.New("failed to initialize sampler")
	}
	defer C.llama_sampler_free(sampler)

	var output strings.Builder
	emitted := 0
	maxTokens := opts.MaxTokens

	for generated := 0; generated < maxTokens; generated++ {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		token := C.llama_sampler_sample(sampler, m.ctx, -1)
		if bool(C.llama_vocab_is_eog(m.vocab, token)) {
			resultCh <- GenerateResult{
				StopReason: StopReasonEndOfGeneration,
				TokenCount: generated,
			}
			return nil
		}

		piece, err := m.tokenToPiece(token)
		if err != nil {
			return err
		}

		output.WriteString(piece)
		visible, stopped := trimStopSuffix(output.String(), opts.Stop)
		if len(visible) > emitted {
			resultCh <- GenerateResult{
				Content:    visible[emitted:],
				TokenCount: generated + 1,
			}
			emitted = len(visible)
		}
		if stopped {
			resultCh <- GenerateResult{
				StopReason: StopReasonStopSequence,
				TokenCount: generated + 1,
			}
			return nil
		}

		C.llama_sampler_accept(sampler, token)

		if m.stopOnCtxFull && len(tokens)+generated+1 >= m.nCtx {
			resultCh <- GenerateResult{
				StopReason: StopReasonContextFull,
				TokenCount: generated + 1,
			}
			return nil
		}

		if err := m.decode([]C.llama_token{token}); err != nil {
			return err
		}
	}

	resultCh <- GenerateResult{
		StopReason: StopReasonMaxTokens,
		TokenCount: maxTokens,
	}
	return nil
}

func (m *Model) tokenize(text string, addSpecial bool) ([]C.llama_token, error) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	required := int(C.llama_tokenize(
		m.vocab,
		cText,
		C.int32_t(len(text)),
		nil,
		0,
		C.bool(addSpecial),
		C.bool(false),
	))
	if required == 0 {
		return nil, nil
	}
	if required < 0 {
		required = -required
	}
	if required <= 0 {
		return nil, errors.New("llama.cpp returned invalid token count")
	}

	tokens := make([]C.llama_token, required)
	actual := int(C.llama_tokenize(
		m.vocab,
		cText,
		C.int32_t(len(text)),
		(*C.llama_token)(unsafe.Pointer(&tokens[0])),
		C.int32_t(len(tokens)),
		C.bool(addSpecial),
		C.bool(false),
	))
	if actual < 0 {
		return nil, fmt.Errorf("llama.cpp tokenization failed: %d", actual)
	}

	return tokens[:actual], nil
}

func (m *Model) decode(tokens []C.llama_token) error {
	if len(tokens) == 0 {
		return nil
	}

	rc := C.llama_decode(
		m.ctx,
		C.llama_batch_get_one(
			(*C.llama_token)(unsafe.Pointer(&tokens[0])),
			C.int32_t(len(tokens)),
		),
	)
	if rc != 0 {
		return fmt.Errorf("llama.cpp decode failed: %d", int(rc))
	}
	return nil
}

func (m *Model) tokenToPiece(token C.llama_token) (string, error) {
	size := 32
	for i := 0; i < 4; i++ {
		buf := make([]byte, size)
		n := int(C.falcon_llama_token_to_piece(
			m.vocab,
			token,
			(*C.char)(unsafe.Pointer(&buf[0])),
			C.int32_t(len(buf)),
		))
		if n >= 0 {
			return string(buf[:n]), nil
		}
		size = -n
		if size <= 0 {
			size *= 2
		}
	}

	return "", errors.New("llama.cpp token_to_piece buffer exhausted")
}

func (m *Model) newSampler(opts GenerateOptions) *C.struct_llama_sampler {
	params := C.llama_sampler_chain_default_params()
	chain := C.llama_sampler_chain_init(params)
	if chain == nil {
		return nil
	}

	topK := opts.TopK
	if topK <= 0 {
		topK = 40
	}
	topP := opts.TopP
	if topP <= 0 || topP > 1 {
		topP = 0.95
	}
	temperature := opts.Temperature
	if temperature < 0 {
		temperature = 0
	}

	C.llama_sampler_chain_add(chain, C.llama_sampler_init_top_k(C.int32_t(topK)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_top_p(C.float(topP), 1))

	if temperature == 0 {
		C.llama_sampler_chain_add(chain, C.llama_sampler_init_greedy())
		return chain
	}

	C.llama_sampler_chain_add(chain, C.llama_sampler_init_temp(C.float(temperature)))

	seed := opts.Seed
	if seed == 0 {
		seed = m.seed
	}
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_dist(C.uint32_t(seed)))
	return chain
}

func (m *Model) resolveGenerateOption(options GenerateOptions) GenerateOptions {
	if options.MaxTokens <= 0 {
		options.MaxTokens = 4000
	}
	if options.Seed == 0 {
		options.Seed = m.seed
	}
	if options.Temperature == 0 {
		options.Temperature = 0.8
	}
	if options.TopP == 0 {
		options.TopP = 0.95
	}
	if options.TopK <= 0 {
		options.TopK = 20
	}
	return options
}

func trimStopSuffix(text string, stops []string) (string, bool) {
	cut := -1
	for _, stop := range stops {
		if stop == "" {
			continue
		}
		idx := strings.Index(text, stop)
		if idx >= 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut >= 0 {
		return text[:cut], true
	}
	return text, false
}
