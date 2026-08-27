# 构建自定义节点

自定义节点可以把业务逻辑放入图中，同时沿用内置节点的验证和检查契约。一个节点由运行时实现和注册表定义两部分组成。

## 1. 实现节点

最小实现是嵌入 `node.Base` 并实现 `Execute`：

```go
type NormalizeNode struct {
    node.Base
    InputPath  state.Path
    OutputPath state.Path
}

func (n *NormalizeNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
    value, ok := access.ReadAny(n.InputPath)
    if !ok {
        return core.NodeResult{}, fmt.Errorf("input is missing")
    }
    text, ok := value.(string)
    if !ok {
        return core.NodeResult{}, fmt.Errorf("expected string input")
    }
    return core.NodeResult{Patch: state.NewPatch(state.PatchOp{
        Kind: state.OpSet, Path: n.OutputPath, Value: strings.TrimSpace(text),
    })}, nil
}
```

通过 `access` 读取和写入状态，不要直接访问完整快照或使用未绑定路径。带外部副作用的操作必须先设计幂等键和
effect-resolution（副作用结果确认）行为，再允许自动重试。

## 2. 声明契约

注册稳定类型、schema 和 State Ports。图通过契约检查后，`builder`（构建器）才会收到已经解析的路径。端口应声明名称、
schema、读写模式、是否必需和描述；结构化状态优先使用 capability contract（能力契约）。

## 3. 注册与构建

```go
reg := builtin.NewDefaultRegistry()
if err := reg.RegisterNodeType(normalizeDefinition()); err != nil {
    log.Fatal(err)
}
graph, err := weaveflow.BuildGraph(reg, definition)
if err != nil {
    log.Fatal(err)
}
```

如果端口契约会变化，请使用带版本的唯一类型名。已有 Graph Definition 引用的是类型字符串，因此修改端口名称或访问模式
属于兼容性变化。

## 契约检查清单

- 每个端口都有 schema、读写模式和清晰描述。
- 必需端口明确标记，可选端口在 `builder` 中验证。
- 并行分支共享目标时声明 reducer（归并器），否则使用互不重叠的路径。
- 为注册验证、图构建、状态行为和错误分类添加针对性测试。
- 使用 `reg.JSONSchema()` 为编辑器导出 schema。

条件和 reducer 使用相同的注册模式；包职责请参见[包结构](/zh/reference/packages)。
