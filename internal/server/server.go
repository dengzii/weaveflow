package server

import (
	"context"
	"falcon/llama_cpp"

	"github.com/gin-gonic/gin"
)

type String string

func (s *String) UnmarshalJSON(bytes []byte) error {
	*s = String(bytes)
	return nil
}

type ModelManager interface {
	Release(id string) error
	Load(id string, path string, backend string) error
	Generate(ctx context.Context, id string, prompt string, options llama_cpp.GenerateOptions) (<-chan llama_cpp.GenerateResult, <-chan error)
}

type CommonResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

type Validatable interface {
	Validate() error
}

type Server struct {
	engine *gin.Engine
}

func NewServer() *Server {
	return &Server{
		engine: gin.New(),
	}
}

func (f *Server) Run() {

	f.engine.Use(gin.Recovery())
	f.engine.Use(gin.Logger())

	modelHub := NewModelManager()
	infer := &interApi{
		modelManager: modelHub,
		items:        map[string]*loadedModel{},
	}
	graph, err := newRunnerApi()
	if err != nil {
		panic(err)
	}

	g := group("",
		group("infer",
			post("model", infer.LoadModel),
			delete_("model/:id", infer.ReleaseModel),
			get("model-list", infer.ModelList),
		),
		group("v1",
			post("chat/completions", infer.Chat),
			get("models", infer.ModelList),
		),
		group("graph",
			get("run", graph.NewRun),
		),
	)

	g.setup(f.engine)

	err = f.engine.Run(":8080")

	if err != nil {
		panic(err)
	}
}
