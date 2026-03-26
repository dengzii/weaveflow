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
	mu               sync.Mutex
	path             string
	model            *C.struct_llama_model
	ctx              *C.struct_llama_context
	vocab            *C.struct_llama_vocab
	nCtx             int
	loadedAt         time.Time
	seed             uint32
	stopOnCtxFull    bool
	chFlushThreshold int
}

type generationPerf struct {
	total        time.Duration
	tokenize     time.Duration
	clear        time.Duration
	prefill      time.Duration
	sample       time.Duration
	piece        time.Duration
	stopCheck    time.Duration
	accept       time.Duration
	decode       time.Duration
	emit         time.Duration
	promptTokens int
	generated    int
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
		return nil, fmt.Errorf("llama_cpp failed to load model: %s", absPath)
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
		return nil, errors.New("llama_cpp failed to initialize context")
	}

	m := &Model{
		path:             absPath,
		model:            model,
		ctx:              ctx,
		vocab:            (*C.struct_llama_vocab)(unsafe.Pointer(C.llama_model_get_vocab(model))),
		nCtx:             int(C.llama_n_ctx(ctx)),
		loadedAt:         time.Now(),
		seed:             uint32(time.Now().UnixNano()),
		stopOnCtxFull:    opts.StopOnContextFull,
		chFlushThreshold: 8,
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

func (m *Model) Release() error {
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

func (m *Model) generate(ctx context.Context, prompt string, opts GenerateOptions, resultCh chan<- GenerateResult) (err error) {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("prompt is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx == nil || m.model == nil || m.vocab == nil {
		return errors.New("model is closed")
	}

	var perf generationPerf
	var sampler *C.struct_llama_sampler
	stopReason := StopReasonNone
	totalStarted := time.Now()
	defer func() {
		perf.total = time.Since(totalStarted)
		if sampler != nil {
			m.logGenerationPerf(perf, sampler, stopReason)
		}
	}()

	tokenizeStarted := time.Now()
	tokens, err := m.tokenize(prompt, opts.AddSpecial)
	perf.tokenize = time.Since(tokenizeStarted)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return errors.New("prompt produced no tokens")
	}
	if len(tokens) >= m.nCtx {
		return fmt.Errorf("prompt token count %d exceeds context size %d", len(tokens), m.nCtx)
	}
	perf.promptTokens = len(tokens)

	clearStarted := time.Now()
	C.llama_memory_clear(C.llama_get_memory(m.ctx), C.bool(true))
	perf.clear = time.Since(clearStarted)

	sampler = m.newSampler(opts)
	if sampler == nil {
		return errors.New("failed to initialize sampler")
	}
	defer C.llama_sampler_free(sampler)

	C.llama_perf_context_reset(m.ctx)
	C.llama_perf_sampler_reset(sampler)

	prefillStarted := time.Now()
	if err := m.decode(tokens); err != nil {
		return err
	}
	perf.prefill = time.Since(prefillStarted)

	matcher := newStopMatcher(opts.Stop)
	pending := make([]byte, 0, m.chFlushThreshold+matcher.maxLen)
	pieceBuf := make([]byte, 64)
	var sampled [1]C.llama_token
	maxTokens := opts.MaxTokens
	flush := func(content []byte, tokenCount int) {
		if len(content) == 0 {
			return
		}

		emittedStarted := time.Now()
		resultCh <- GenerateResult{
			Content:    string(content),
			TokenCount: tokenCount,
		}
		perf.emit += time.Since(emittedStarted)
	}
	sendStop := func(reason string, tokenCount int) {
		emittedStarted := time.Now()
		resultCh <- GenerateResult{
			StopReason: reason,
			TokenCount: tokenCount,
		}
		perf.emit += time.Since(emittedStarted)
	}

	for generated := 0; generated < maxTokens; generated++ {
		if ctx != nil {
			select {
			case <-ctx.Done():
				stopReason = StopReasonCancelled
				return ctx.Err()
			default:
			}
		}

		sampleStarted := time.Now()
		token := C.llama_sampler_sample(sampler, m.ctx, -1)
		perf.sample += time.Since(sampleStarted)
		if bool(C.llama_vocab_is_eog(m.vocab, token)) {
			perf.generated = generated
			flush(pending, generated)
			pending = pending[:0]
			stopReason = StopReasonEndOfGeneration
			sendStop(stopReason, generated)
			return nil
		}

		pieceStarted := time.Now()
		var piece []byte
		pieceBuf, piece, err = m.tokenToPieceBytes(token, pieceBuf)
		perf.piece += time.Since(pieceStarted)
		if err != nil {
			return err
		}

		stopStarted := time.Now()
		previousPendingLen := len(pending)
		pending = append(pending, piece...)
		searchStart := previousPendingLen - matcher.maxLen + 1
		stopIndex, stopped := matcher.find(pending, searchStart)
		perf.stopCheck += time.Since(stopStarted)
		if stopped {
			perf.generated = generated + 1
			flush(pending[:stopIndex], generated+1)
			pending = pending[:0]
			stopReason = StopReasonStopSequence
			sendStop(stopReason, generated+1)
			return nil
		}

		if safe := len(pending) - matcher.holdback(); safe >= m.chFlushThreshold {
			flush(pending[:safe], generated+1)
			copy(pending, pending[safe:])
			pending = pending[:len(pending)-safe]
		}

		acceptStarted := time.Now()
		C.llama_sampler_accept(sampler, token)
		perf.accept += time.Since(acceptStarted)

		if m.stopOnCtxFull && len(tokens)+generated+1 >= m.nCtx {
			perf.generated = generated + 1
			flush(pending, generated+1)
			pending = pending[:0]
			stopReason = StopReasonContextFull
			sendStop(stopReason, generated+1)
			return nil
		}

		sampled[0] = token
		decodeStarted := time.Now()
		if err := m.decode(sampled[:]); err != nil {
			return err
		}
		perf.decode += time.Since(decodeStarted)
		perf.generated = generated + 1
	}

	perf.generated = maxTokens
	flush(pending, maxTokens)
	stopReason = StopReasonMaxTokens
	sendStop(stopReason, maxTokens)
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
		return nil, errors.New("llama_cpp returned invalid token count")
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
		return nil, fmt.Errorf("llama_cpp tokenization failed: %d", actual)
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
		return fmt.Errorf("llama_cpp decode failed: %d", int(rc))
	}
	return nil
}

func (m *Model) tokenToPieceBytes(token C.llama_token, scratch []byte) ([]byte, []byte, error) {
	if len(scratch) == 0 {
		scratch = make([]byte, 32)
	}

	for i := 0; i < 4; i++ {
		n := int(C.falcon_llama_token_to_piece(
			m.vocab,
			token,
			(*C.char)(unsafe.Pointer(&scratch[0])),
			C.int32_t(len(scratch)),
		))
		if n >= 0 {
			return scratch, scratch[:n], nil
		}

		size := -n
		if size <= len(scratch) {
			size = len(scratch) * 2
		}
		scratch = make([]byte, size)
	}

	return scratch, nil, errors.New("llama_cpp token_to_piece buffer exhausted")
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
	return resolveGenerateOptions(m.seed, options)
}

func (m *Model) logGenerationPerf(perf generationPerf, sampler *C.struct_llama_sampler, stopReason string) {
	ctxPerf := C.llama_perf_context(m.ctx)
	samplerPerf := C.llama_perf_sampler(sampler)

	logger.Debug("generation finished",
		zap.String("stop_reason", stopReason),
		zap.Int("prompt_tokens", perf.promptTokens),
		zap.Int("generated_tokens", perf.generated),
		zap.Duration("go_total", perf.total),
		zap.Duration("go_tokenize", perf.tokenize),
		zap.Duration("go_clear", perf.clear),
		zap.Duration("go_prefill", perf.prefill),
		zap.Duration("go_sample", perf.sample),
		zap.Duration("go_piece", perf.piece),
		zap.Duration("go_stop_check", perf.stopCheck),
		zap.Duration("go_accept", perf.accept),
		zap.Duration("go_decode", perf.decode),
		zap.Duration("go_emit", perf.emit),
		zap.Float64("llama_prompt_eval_ms", float64(ctxPerf.t_p_eval_ms)),
		zap.Float64("llama_eval_ms", float64(ctxPerf.t_eval_ms)),
		zap.Int("llama_prompt_eval_tokens", int(ctxPerf.n_p_eval)),
		zap.Int("llama_eval_tokens", int(ctxPerf.n_eval)),
		zap.Float64("llama_sample_ms", float64(samplerPerf.t_sample_ms)),
		zap.Int("llama_sample_tokens", int(samplerPerf.n_sample)),
	)
}
