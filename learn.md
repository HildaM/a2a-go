# A2A-Go 项目学习文档

## 项目概述

a2a-go 是一个用 Go 语言实现的 Agent-to-Agent (A2A) 通信协议服务器库。该项目提供了构建符合 A2A 协议的代理服务器的灵活框架，支持代理间的异步通信、任务管理和实时流式传输。

### 核心特性

- **A2A 协议合规性**: 实现核心 A2A RPC 方法和代理发现端点
- **灵活的处理器配置**: 通过 `HandlerFuncs` 结构体配置所需的 RPC 方法
- **函数式选项模式**: 使用选项模式配置服务器参数
- **可插拔任务存储**: 支持内存存储和文件存储
- **服务器推送事件 (SSE)**: 支持实时任务更新
- **默认实现**: 提供默认的任务处理逻辑

## 项目架构

```mermaid
graph TB
    subgraph "A2A-Go 架构"
        Client["A2A 客户端"]
        Server["A2A 服务器"]
        
        subgraph "核心组件"
            Handler["Handler 接口"]
            Store["TaskStore 接口"]
            Schema["Schema 定义"]
        end
        
        subgraph "具体实现"
            HandlerFuncs["HandlerFuncs"]
            BaseHandler["BaseHandler"]
            InMemoryStore["InMemoryTaskStore"]
            FileStore["FileTaskStore"]
        end
        
        subgraph "示例应用"
            HelloWorld["HelloWorld 示例"]
            Simple["Simple 示例"]
            A2AServer["A2AServer 主程序"]
        end
    end
    
    Client -->|HTTP/JSON-RPC| Server
    Server --> Handler
    Server --> Store
    Handler --> HandlerFuncs
    Handler --> BaseHandler
    Store --> InMemoryStore
    Store --> FileStore
    
    HelloWorld -.-> HandlerFuncs
    Simple -.-> HandlerFuncs
    A2AServer -.-> HandlerFuncs
```

## 核心模块分析

### 1. Schema 模块 (`schema.go`)

这是项目的核心数据结构定义模块，包含了所有 A2A 协议相关的类型定义。

#### 关键数据结构

```mermaid
classDiagram
    class Task {
        +string ID
        +string SessionID
        +TaskStatus Status
        +Artifact[] Artifacts
        +Metadata map
    }
    
    class TaskStatus {
        +TaskState State
        +Message Message
        +string Timestamp
        +"SetTimestamp(time.Time)"
    }
    
    class Message {
        +Role Role
        +Part[] Parts
        +Metadata map
        +"MarshalJSON() ([]byte, error)"
        +"UnmarshalJSON([]byte) error"
    }
    
    class Part {
        <<interface>>
        +"Type() string"
    }
    
    class TextPart {
        +string Text
        +"Type() string"
    }
    
    class FilePart {
        +FileData File
        +string MimeType
        +"Type() string"
    }
    
    class Artifact {
        +string Name
        +string Description
        +Part[] Parts
        +Metadata map
        +int Index
        +bool LastChunk
        +bool Append
    }
    
    Task --> TaskStatus
    Task --> Artifact
    TaskStatus --> Message
    Message --> Part
    Part <|-- TextPart
    Part <|-- FilePart
```

**核心设计特点:**
- 使用接口 `Part` 实现多态，支持不同类型的消息内容
- 自定义 JSON 序列化/反序列化处理复杂的接口类型
- 任务状态机设计，支持 `pending`、`processing`、`completed`、`failed`、`canceled` 等状态

### 2. Server 模块 (`server.go`)

服务器模块是整个框架的核心，负责 HTTP 服务、RPC 处理和 SSE 流式传输。

```mermaid
sequenceDiagram
    participant Client as A2A 客户端
    participant Server as A2A 服务器
    participant Handler as 处理器
    participant Store as 任务存储
    
    Note over Client,Store: 任务发送流程
    Client->>Server: POST /rpc (tasks/send)
    Server->>Server: 解析 JSON-RPC 请求
    Server->>Store: 创建/加载任务
    Server->>Handler: 调用 SendTask
    Handler->>Handler: 处理业务逻辑
    Handler->>Server: 返回完成的任务
    Server->>Store: 保存任务结果
    Server->>Client: 返回任务状态
    
    Note over Client,Store: 流式任务订阅流程
    Client->>Server: POST /rpc (tasks/sendSubscribe)
    Server->>Store: 创建待处理任务
    Server->>Client: 建立 SSE 连接
    Server->>Handler: 调用 SendTaskSubscribe
    Handler->>Handler: 异步处理任务
    loop 任务处理中
        Handler->>Server: UpdateFn(状态/工件更新)
        Server->>Store: 更新任务状态
        Server->>Client: SSE 事件推送
    end
```

**关键功能实现:**

1. **函数式选项模式**: 通过 `WithAddress`、`WithLogger`、`WithStore` 等选项配置服务器
2. **适配器模式**: `handlerAdapter` 将 `HandlerFuncs` 适配为 `Handler` 接口
3. **SSE 流式传输**: 支持实时任务状态和工件更新推送
4. **任务生命周期管理**: 从创建到完成的完整任务状态管理

### 3. Handler 模块 (`handler.go`)

处理器模块定义了业务逻辑接口和默认实现。

```mermaid
graph LR
    subgraph "Handler 架构"
        Interface["Handler 接口"]
        
        subgraph "实现方式"
            HandlerFuncs["HandlerFuncs 结构体"]
            BaseHandler["BaseHandler 默认实现"]
        end
        
        subgraph "适配器"
            Adapter["handlerAdapter"]
        end
    end
    
    Interface --> Adapter
    HandlerFuncs --> Adapter
    BaseHandler --> Adapter
    
    Adapter -.->|"如果函数为 nil"| BaseHandler
    Adapter -.->|"如果函数存在"| HandlerFuncs
```

**设计模式分析:**
- **策略模式**: `HandlerFuncs` 允许用户只实现需要的方法
- **模板方法模式**: `BaseHandler` 提供默认实现
- **适配器模式**: `handlerAdapter` 统一接口

### 4. Store 模块 (`store.go`)

存储模块提供了任务持久化的抽象接口和具体实现。

```mermaid
classDiagram
    class TaskStore {
        <<interface>>
        +Save(*TaskAndHistory) error
        +Load(string) (*TaskAndHistory, error)
        +Delete(string) error
    }
    
    class InMemoryTaskStore {
        -sync.RWMutex mu
        -map[string]*TaskAndHistory store
        +Save(*TaskAndHistory) error
        +Load(string) (*TaskAndHistory, error)
        +Delete(string) error
    }
    
    class FileTaskStore {
        -sync.RWMutex mu
        -string baseDir
        +Save(*TaskAndHistory) error
        +Load(string) (*TaskAndHistory, error
        +Delete(string) error
    }
    
    class TaskAndHistory {
        +*Task Task
        +[]Message History
    }
    
    TaskStore <|-- InMemoryTaskStore
    TaskStore <|-- FileTaskStore
    TaskStore --> TaskAndHistory
```

**存储策略:**
- **内存存储**: 适用于开发和测试，性能最佳但不持久
- **文件存储**: 适用于生产环境，支持原子写入和持久化
- **可扩展性**: 通过接口设计支持数据库等其他存储后端

## 任务处理流程

### 同步任务处理

```mermaid
flowchart TD
    Start(["客户端发送任务"]) --> Parse["解析 JSON-RPC 请求"]
    Parse --> Create["创建/加载任务"]
    Create --> Save1["保存初始状态"]
    Save1 --> Handler["调用处理器"]
    Handler --> Process["执行业务逻辑"]
    Process --> Complete["任务完成"]
    Complete --> Save2["保存最终状态"]
    Save2 --> Response["返回结果"]
    Response --> End(["结束"])
    
    style Start fill:#e1f5fe
    style End fill:#e8f5e8
    style Process fill:#fff3e0
```

### 异步流式任务处理

```mermaid
flowchart TD
    Start(["客户端订阅任务"]) --> Parse["解析订阅请求"]
    Parse --> Create["创建待处理任务"]
    Create --> SSE["建立 SSE 连接"]
    SSE --> Async["启动异步处理"]
    
    subgraph "异步处理循环"
        Process["处理业务逻辑"]
        Update["发送状态更新"]
        Check{"任务完成?"}
        Process --> Update
        Update --> Check
        Check -->|"否"| Process
    end
    
    Async --> Process
    Check -->|"是"| Final["发送最终状态"]
    Final --> Close["关闭 SSE 连接"]
    Close --> End(["结束"])
    
    style Start fill:#e1f5fe
    style End fill:#e8f5e8
    style Process fill:#fff3e0
    style Update fill:#f3e5f5
```

## 示例应用分析

### HelloWorld 示例

最简单的 A2A 代理实现，展示了基本的同步任务处理：

```go
// 核心处理逻辑
func handleHelloWorldTask(ctx *a2a.TaskContext) (*a2a.Task, error) {
    // 创建响应消息
    helloMessage := a2a.Message{
        Role:  a2a.RoleAgent,
        Parts: []a2a.Part{a2a.TextPart{Text: "Hello World!"}},
    }
    
    // 更新任务状态为完成
    finalTask := ctx.CurrentTask
    finalTask.Status.State = a2a.StateCompleted
    finalTask.Status.Message = &helloMessage
    finalTask.Status.SetTimestamp(time.Now())
    
    return finalTask, nil
}
```

### Simple 示例

展示了更复杂的同步任务处理，包括输入解析和工件生成：

```go
func handleSimpleEchoTask(ctx *a2a.TaskContext) (*a2a.Task, error) {
    // 解析输入
    inputText := "(No text part found)"
    if len(ctx.Message.Parts) > 0 {
        if textPart, ok := ctx.Message.Parts[0].(a2a.TextPart); ok {
            inputText = textPart.Text
        }
    }
    
    // 生成响应和工件
    responseText := fmt.Sprintf("Echoing back: '%s'", inputText)
    completionMessage := a2a.Message{
        Role:  a2a.RoleAgent,
        Parts: []a2a.Part{a2a.TextPart{Text: responseText}},
    }
    
    responseArtifact := a2a.Artifact{
        Name:  "echo_result",
        Parts: []a2a.Part{a2a.TextPart{Text: fmt.Sprintf("Processed: %s", inputText)}},
    }
    
    // 更新任务状态
    finalTask := ctx.CurrentTask
    finalTask.Status.State = a2a.StateCompleted
    finalTask.Status.Message = &completionMessage
    finalTask.Status.SetTimestamp(time.Now())
    finalTask.Artifacts = []a2a.Artifact{responseArtifact}
    
    return finalTask, nil
}
```

### A2AServer 主程序

展示了流式任务处理的完整实现：

```go
func handleSendTaskSubscribe(ctx *a2a.TaskContext) (*a2a.Task, error) {
    // 启动异步处理
    go func() {
        // 发送处理中状态
        workingStatus := a2a.TaskStatus{State: a2a.StateProcessing}
        ctx.UpdateFn(workingStatus)
        
        // 发送工件
        artifact := a2a.Artifact{
            Parts: []a2a.Part{a2a.TextPart{Text: "Processed input"}},
        }
        ctx.UpdateFn(artifact)
        
        // 发送完成状态
        completedStatus := a2a.TaskStatus{
            State:   a2a.StateCompleted,
            Message: &completionMessage,
        }
        ctx.UpdateFn(completedStatus)
    }()
    
    return ctx.CurrentTask, nil
}
```

## 协议实现细节

### JSON-RPC 2.0 支持

项目完全实现了 JSON-RPC 2.0 规范：

```mermaid
sequenceDiagram
    participant Client as 客户端
    participant Server as 服务器
    
    Note over Client,Server: JSON-RPC 请求/响应流程
    
    Client->>Server: {
    Note right of Client: "jsonrpc": "2.0",<br/>"method": "tasks/send",<br/>"params": {...},<br/>"id": 1
    
    Server->>Server: 验证请求格式
    Server->>Server: 路由到对应方法
    Server->>Server: 执行业务逻辑
    
    Server->>Client: {
    Note left of Server: "jsonrpc": "2.0",<br/>"result": {...},<br/>"id": 1
    
    Note over Client,Server: 错误处理
    Client->>Server: 无效请求
    Server->>Client: {
    Note left of Server: "jsonrpc": "2.0",<br/>"error": {<br/>  "code": -32600,<br/>  "message": "Invalid Request"<br/>},<br/>"id": null
```

### 代理发现机制

通过 `/.well-known/agent.json` 端点实现代理发现：

```json
{
  "name": "Go Simple Example Agent",
  "description": "A basic Go agent demonstrating the a2a-go library.",
  "url": "http://localhost:8080",
  "version": "0.1.0",
  "capabilities": {
    "streaming": true,
    "pushNotifications": false
  },
  "authentication": {
    "schemes": ["None"]
  },
  "skills": [
    {
      "id": "simple_echo",
      "name": "Simple Echo Task",
      "description": "Echoes back the input message"
    }
  ]
}
```

## 设计模式总结

1. **策略模式**: `HandlerFuncs` 允许灵活配置处理策略
2. **适配器模式**: `handlerAdapter` 统一不同的处理器实现
3. **模板方法模式**: `BaseHandler` 提供默认实现模板
4. **观察者模式**: SSE 机制实现任务状态变化通知
5. **工厂模式**: `NewServer` 函数创建配置好的服务器实例
6. **接口隔离**: `TaskStore`、`Handler` 等接口职责单一
7. **依赖注入**: 通过函数式选项注入依赖

## 扩展性分析

### 存储后端扩展

```mermaid
graph LR
    TaskStore["TaskStore 接口"]
    
    subgraph "现有实现"
        Memory["InMemoryTaskStore"]
        File["FileTaskStore"]
    end
    
    subgraph "可扩展实现"
        Redis["RedisTaskStore"]
        DB["DatabaseTaskStore"]
        S3["S3TaskStore"]
    end
    
    TaskStore --> Memory
    TaskStore --> File
    TaskStore -.-> Redis
    TaskStore -.-> DB
    TaskStore -.-> S3
```

### 处理器扩展

```mermaid
graph TB
    Handler["Handler 接口"]
    
    subgraph "业务处理器"
        AI["AI 处理器"]
        Workflow["工作流处理器"]
        Integration["集成处理器"]
    end
    
    subgraph "中间件"
        Auth["认证中间件"]
        Log["日志中间件"]
        Rate["限流中间件"]
    end
    
    Handler --> AI
    Handler --> Workflow
    Handler --> Integration
    
    AI --> Auth
    Workflow --> Log
    Integration --> Rate
```

## 性能考虑

1. **并发安全**: 所有存储实现都使用读写锁保证并发安全
2. **内存管理**: 任务和历史记录的深拷贝避免数据竞争
3. **流式传输**: SSE 支持大量并发连接的实时更新
4. **错误处理**: 完善的错误处理和恢复机制
5. **资源清理**: 自动清理断开的 SSE 连接

## 总结

a2a-go 项目是一个设计良好的 A2A 协议实现，具有以下优点：

- **模块化设计**: 清晰的模块分离和职责划分
- **可扩展性**: 通过接口和适配器模式支持灵活扩展
- **易用性**: 提供简单的 API 和丰富的示例
- **标准合规**: 完全符合 A2A 协议规范
- **生产就绪**: 支持并发、错误处理和资源管理

该项目为构建 Agent-to-Agent 通信系统提供了坚实的基础，可以作为学习现代 Go 项目架构和协议实现的优秀案例。