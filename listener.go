package weaveflow

import (
	"context"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

type LoggingListener struct {
	*langgraph.LoggingListener
}

func NewLoggingListener() *LoggingListener {
	return &LoggingListener{LoggingListener: langgraph.NewLoggingListener()}

}

func (l LoggingListener) OnNodeEvent(ctx context.Context, event langgraph.NodeEvent, nodeName string, state State, err error) {
	l.LoggingListener.OnNodeEvent(ctx, event, nodeName, state, err)
}
