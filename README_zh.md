# a2aserver - Go A2A 服务器库

[![Go Reference](https://pkg.go.dev/badge/github.com/a2aserver/a2a-go.svg)](https://pkg.go.dev/github.com/a2aserver/a2a-go)

本仓库包含一个 Go 库 (`github.com/a2aserver/a2a-go`)，用于构建实现 Agent-to-Agent (A2A) 通信协议的服务器，以及示例服务器实现 (`cmd/a2aserver`, `examples/`)。官方 A2A 协议规范请参阅 [https://google.github.io/A2A/](https://google.github.io/A2A/)。

目标是提供一个灵活且易于使用的框架，用于在 Go 中创建 A2A Agent。

## 特性

*   **A2A 协议兼容:** 实现核心 A2A RPC 方法 (`tasks/send`, `tasks/get`, `tasks/cancel`, `tasks/sendSubscribe` 等) 以及 Agent 发现端点 (`/.well-known/agent.json`)。
*   **灵活的处理器配置:** 使用 `HandlerFuncs` 结构体，仅为您 Agent 需要处理的 RPC 方法提供实现。服务器通过 `BaseHandler` 为其他方法提供合理的默认值。
*   **函数式选项:** 使用 `Option` 模式 (`WithAddress`, `WithLogger`, `WithStore`, `WithBasePath`) 配置服务器（监听地址、日志记录器、任务存储、RPC 基础路径）。
*   **可配置的基础路径:** 使用 `WithBasePath` 设置 RPC 端点的基础路径（默认为 `/`）。
*   **可插拔的任务存储:** 包含 `InMemoryTaskStore` (默认) 和 `FileTaskStore` (具有原子保存功能)。通过实现 `TaskStore` 接口定义您自己的存储方式。
*   **服务器发送事件 (SSE):** 支持 `tasks/sendSubscribe` 和 `tasks/resubscribe` 方法的实时任务更新。
*   **默认实现:** 为 `SendTask`、`SendTaskSubscribe` (创建待处理任务)、`GetTask`、`CancelTask` 和 `Resubscribe` 提供默认逻辑，这些逻辑会自动与配置的 `TaskStore` 交互。
*   **统一的 Schema:** 核心 A2A 数据结构 (Task, Message, Part, Artifact 等) 定义在 `schema.go` 中。

## 快速开始

### 先决条件

*   Go 1.18 或更高版本

### 构建

构建示例服务器：

```bash
go build ./cmd/a2aserver/...
go build ./examples/.../...
```

### 运行示例

*   **HelloWorld 示例 (简单的 SendTask):**
    ```bash
    go run ./examples/helloworld/main.go
    # 或 ./helloworld
    ```
    使用以下命令测试：
    ```bash
    # 获取 Agent Card
    curl http://localhost:8080/.well-known/agent.json | jq .
    # 发送任务 (假设默认基础路径为 /)
    curl -X POST http://localhost:8080/ -d '{"jsonrpc":"2.0","method":"tasks/send","params":{"message":{"role":"user","parts":[{"type":"text","text":"Hi"}]}},"id":1}' | jq .
    ```

*   **Simple 示例 (流式 SendTaskSubscribe):**
    ```bash
    go run ./examples/simple/main.go
    # 或 ./simple
    ```
    使用以下命令测试：
    ```bash
    # 发送流式任务 (保持连接打开以接收 SSE，假设默认基础路径为 /)
    curl -X POST http://localhost:8080/ -d '{"jsonrpc":"2.0","method":"tasks/sendSubscribe","params":{"message":{"role":"user","parts":[{"type":"text","text":"Stream test"}]}},"id":2}'
    ```

## 使用方法 (库 `github.com/a2aserver/a2a-go`)

以下是使用该库的基本示例：

```go
package main

import (
	"log"
	"github.com/a2aserver/a2a-go" // 调整导入路径
	"os"
)

// 1. 实现必需的 GetAgentCardFunc
func myAgentCard() (*a2a.AgentCard, error) {
	// ... 返回您的 Agent Card ...
	return &a2a.AgentCard{
		Name: "我的自定义 Agent",
		// ... 其他字段 ...
	}, nil
}

// 2. 实现可选的处理器函数 (例如 SendTaskFunc)
// 通过 ctx.CurrentTask 访问当前任务状态
// 对于流式处理 (SendTaskSubscribeFunc)，使用 ctx.UpdateFn 发送更新。
func mySendTask(ctx *a2a.TaskContext) (*a2a.Task, error) {
	log.Printf("正在处理任务 %s", ctx.ID)
	// ... 您的逻辑，可能会修改 ctx.CurrentTask ...
	finalTask := ctx.CurrentTask // 使用提供的任务作为基础
	finalTask.Status.State = a2a.StateCompleted
	// ... 设置状态消息/工件 ...
	log.Printf("任务 %s 已由自定义处理器完成。", ctx.ID)
	return finalTask, nil
}

func main() {
	logger := log.New(os.Stderr, "[MyAgent] ", log.LstdFlags)

	// 3. 定义 HandlerFuncs
	handlerFuncs := a2a.HandlerFuncs{
		GetAgentCardFunc: myAgentCard,
		SendTaskFunc:     mySendTask,
		// 其他函数如 SendTaskSubscribeFunc, GetTaskFunc, CancelTaskFunc
		// 保留为 nil 将使用服务器默认值 (与 TaskStore 交互)。
	}

	// 4. 创建并配置服务器
	store, err := a2a.NewFileTaskStore("my_agent_tasks") // 使用文件存储
	if err != nil {
		logger.Fatalf("创建文件存储失败: %v", err)
	}

	server, err := a2a.NewServer(
		handlerFuncs,
		a2a.WithAddress(":8080"),
		a2a.WithLogger(logger),
		a2a.WithStore(store),
		// a2a.WithBasePath("/myagent/rpc"), // 可选：设置自定义 RPC 基础路径
	)
	if err != nil {
		logger.Fatalf("创建服务器失败: %v", err)
	}

	// 5. 启动服务器 (如果需要，添加优雅关闭)
	logger.Println("正在启动服务器...")
	if err := server.Serve(); err != nil {
		logger.Fatalf("服务器失败: %v", err)
	}
}
```
