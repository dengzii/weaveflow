//go:build cgo

package memory

import (
	"sync"

	"github.com/yanyiwu/gojieba"
)

var (
	jiebaOnce sync.Once
	jiebaInst *gojieba.Jieba
)

func segmentText(text string) []string {
	jiebaOnce.Do(func() {
		jiebaInst = gojieba.NewJieba()
	})
	return jiebaInst.CutForSearch(text, true)
}
