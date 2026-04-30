package server

import (
	"context"
	"weaveflow/internal/neo"
	"weaveflow/llama_cpp"

	"weaveflow/llms/openai"

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
			post("run", graph.NewRun),
			get("run", graph.NewRun),
		),
	)

	g.setup(f.engine)

	neoModel, err := openai.New()
	if err != nil {
		panic(err)
	}
	neoCfg := neo.DefaultConfig()
	neoBuildCtx := neo.NewBuildContext(neoModel, "neo_data")
	neoServer := neo.NewServer(neoBuildCtx, neoCfg, "neo_data")
	neoServer.RegisterRoutes(f.engine.Group("/neo"))

	err = f.engine.Run(":8080")

	if err != nil {
		panic(err)
	}
}
