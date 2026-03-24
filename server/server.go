package server

import (
	"github.com/gin-gonic/gin"
)

type String string

func (s *String) UnmarshalJSON(bytes []byte) error {
	*s = String(bytes)
	return nil
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

	infer := &Infer{}

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
	)

	g.setup(f.engine)

	err := f.engine.Run(":8080")

	if err != nil {
		panic(err)
	}
}
