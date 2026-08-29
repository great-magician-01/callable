# callable 文档

**中文** | [English](../en/README.md)

按功能拆分的详细使用文档。所有 API 名称与签名以根包 [`callable.go`](../../callable.go) 为准。

## 目录

- [快速开始](getting-started.md) — 安装、创建 Client 与三种 Provider、内置端点常量、Compat 方言嗅探
- [消息模型](messages.md) — Message/Part 统一模型、构造器、历史回传保真、JSON 持久化
- [Agent 循环](agent.md) — Run/RunStream、审批钩子、并行工具执行、max turns、配置层级
- [多轮会话](session.md) — Session、Ask/AskStream、上下文窗口与自动/手动压缩、历史持久化与恢复
- [工具](tools.md) — NewTool 泛型构造、JSON Schema 生成、NewRawTool、错误回传
- [联网搜索](web-search.md) — provider 内置搜索自动嗅探、Kimi 回显协议、Tavily 回退
- [流式事件](streaming.md) — 事件类型一览、典型事件序列、Usage 统计
- [思考模式](thinking.md) — Effort 档位、各 provider 映射表、国产端点的坑
- [结构化输出与采样参数](structured-output.md) — JSON 模式 / JSON Schema / DecodeJSON、top_p 与停止序列
- [Skill 渐进披露](skills.md) — read_skill 内置工具、读取钩子、自定义加载工具
- [子代理](subagents.md) — load_agent 两步委派、SubAgentOption、事件透传
- [图片输入](images.md) — 本地路径/URL/字节、图文混排、跨 provider 格式转换
- [错误处理](errors.md) — APIError、自动重试、取消与超时、WithExtra 逃生舱

## 其他

- [设计方案 PLAN.md](../PLAN.md) — 架构与 wire 格式映射的设计文档
- [examples/](../../examples) — 可运行示例
