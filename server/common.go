package server

import (
	"errors"

	"github.com/gin-gonic/gin"
)

var (
	errorInvalidParam = errors.New("invalid parameter")
	errorUnauthorized = errors.New("unauthorized")
)

type commonResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

func responseSuccess(ctx *gin.Context, data interface{}) error {

	ctx.JSON(200, commonResponse{
		Code: 200,
		Msg:  "ok",
		Data: data,
	})
	return nil
}
