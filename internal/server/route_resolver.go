package server

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
)

var typeGinContext = reflect.TypeOf((*gin.Context)(nil))
var typeError = reflect.TypeOf((*error)(nil)).Elem()

type iRoute interface {
	setup(parent gin.IRouter)
}

type routeInfo struct {
	method       string
	relativePath string
	handlerFunc  interface{}
	Condition    string

	valOfHandleFn  reflect.Value
	typeOfHandleFn reflect.Type
	typeArg1       reflect.Type
	typeReturn0    reflect.Type
	args           int
	validate       bool
}

type routeGroup struct {
	name   string
	routes []iRoute
}

type routeUse struct {
	middleware []gin.HandlerFunc
	routes     []iRoute
}

func (that *routeGroup) setup(parent gin.IRouter) {
	p := parent.Group(that.name)
	for _, rt := range that.routes {
		rt.setup(p)
	}
}

func (that *routeUse) setup(parent gin.IRouter) {
	parent.Use(that.middleware...)
	for _, rt := range that.routes {
		rt.setup(parent)
	}
}

func (that *routeInfo) setup(parent gin.IRouter) {
	that.reflectHandleFn()
	that.registerHandle(parent)
}

func (that *routeInfo) reflectHandleFn() {

	that.valOfHandleFn = reflect.ValueOf(that.handlerFunc)
	that.typeOfHandleFn = reflect.TypeOf(that.handlerFunc)
	if that.typeOfHandleFn.Kind() != reflect.Func {
		that.panic("the routeInfo handlerFunc must be a function")
	}

	that.args = that.typeOfHandleFn.NumIn()
	if that.args == 0 || that.args > 2 {
		that.panic("route handleFunc incorrect arguments quantity")
	}
	typeArg0 := that.typeOfHandleFn.In(0)
	if !typeArg0.AssignableTo(typeGinContext) {
		that.panic("route handleFunc incorrect type of arg 0: " + typeArg0.String())
	}

	if that.args == 2 {
		that.typeArg1 = that.typeOfHandleFn.In(1)
		if that.typeArg1.Kind() != reflect.Ptr {
			that.panic("the second arg must be pointer")
		}
		if that.typeArg1.Elem().Kind() != reflect.Struct {
			that.panic("the second arg must be struct")
		}
		that.typeArg1 = that.typeArg1.Elem()
	}

	if that.typeOfHandleFn.NumOut() != 1 {
		that.panic("must return just one error")
	}
	typeReturn0 := that.typeOfHandleFn.Out(0)
	if !typeReturn0.AssignableTo(typeError) {
		that.panic("must return an error:" + typeReturn0.Name())
	}
}

func (that *routeInfo) registerHandle(parent gin.IRouter) {

	parent.Handle(that.method, that.relativePath, func(ctx *gin.Context) {

		c := ctx
		inVal := []interface{}{c}

		if that.args == 2 {

			arg1 := reflect.New(that.typeArg1).Interface()
			err := ctx.BindJSON(arg1)
			if err != nil {
				onParamError(ctx, err)
				return
			}
			if that.validate {
				err = arg1.(Validatable).Validate()
				if err != nil {
					onParamValidateFailed(ctx, err)
					return
				}
			}
			inVal = append(inVal, arg1)
		} else {
			//
		}
		err := that.callRealHandleFunc(inVal...)
		if err != nil {
			onHandlerFuncErr(ctx, err)
		}
	})
}

func (that *routeInfo) callRealHandleFunc(in ...interface{}) error {

	var val []reflect.Value
	for _, i2 := range in {
		val = append(val, reflect.ValueOf(i2))
	}
	err := that.valOfHandleFn.Call(val)[0].Interface()
	if err != nil {
		return err.(error)
	}
	return nil
}

func (that *routeInfo) panic(reason string) string {
	panic(fmt.Sprintf("reason: %s, path: %s, func: %s", reason, that.relativePath, that.typeOfHandleFn))
}

func group(name string, r ...iRoute) *routeGroup {
	return &routeGroup{
		name:   name,
		routes: r,
	}
}

func use(middleware gin.HandlerFunc, r ...iRoute) *routeUse {
	return &routeUse{
		middleware: []gin.HandlerFunc{middleware},
		routes:     r,
	}
}

func post(relativePath string, handlerFunc interface{}) *routeInfo {
	return handle(http.MethodPost, relativePath, handlerFunc)
}

func get(relativePath string, handlerFunc interface{}) *routeInfo {
	return handle(http.MethodGet, relativePath, handlerFunc)
}

func delete_(relativePath string, handlerFunc interface{}) *routeInfo {
	return handle(http.MethodDelete, relativePath, handlerFunc)
}

func patch(relativePath string, handlerFunc interface{}) *routeInfo {
	return handle(http.MethodPatch, relativePath, handlerFunc)
}

func put(relativePath string, handlerFunc interface{}) *routeInfo {
	return handle(http.MethodPut, relativePath, handlerFunc)
}

func head(relativePath string, handlerFunc interface{}) *routeInfo {
	return handle(http.MethodHead, relativePath, handlerFunc)
}

func handle(method, relativePath string, handlerFunc interface{}) *routeInfo {
	return &routeInfo{
		method:       method,
		relativePath: relativePath,
		handlerFunc:  handlerFunc,
	}
}

func (that *routeInfo) withValidate() *routeInfo {
	that.validate = true
	return that
}

func onHandlerValidateErr(ctx *gin.Context, err error) {
	ctx.JSON(http.StatusOK, CommonResponse{
		Code: 40001,
		Msg:  err.Error(),
		Data: nil,
	})
	return
}

func onParamValidateFailed(ctx *gin.Context, err error) {
	ctx.JSON(http.StatusOK, CommonResponse{
		Code: 300,
		Msg:  "invalid parameter:" + err.Error(),
		Data: nil,
	})
}

func onParamError(ctx *gin.Context, err error) {
	ctx.JSON(http.StatusOK, CommonResponse{
		Code: 300,
		Msg:  "parameter parse error: " + err.Error(),
		Data: nil,
	})
}

func onHandlerFuncErr(ctx *gin.Context, err error) {
	ctx.JSON(http.StatusOK, CommonResponse{
		Code: 500,
		Msg:  err.Error(),
		Data: nil,
	})
}
