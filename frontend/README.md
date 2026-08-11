# EffChat 前端

EffChat 的 Web 前端：轻量 AI 对话界面，流式输出、代码块工作区、多模型切换。

## 技术栈

- **构建**：Vite + React 19 + TypeScript
- **UI**：Shadcn/ui（Radix）+ Tailwind CSS v4
- **状态**：Zustand
- **动画**：CSS + tailwindcss-animate
- **流式渲染**：react-markdown + remark（恒速打字机 + 淡入）

## 本地开发

完整服务需要 PostgreSQL、migration、Python extractor、Go backend 和 Web 前端。
请在仓库根目录按 [CONTRIBUTING.md](../CONTRIBUTING.md#local-setup) 使用 Docker Compose
启动完整、可健康检查的开发栈。

只开发前端时，先确保 backend 已在 `http://localhost:8080` 运行，再启动 Vite：

```bash
npm ci
npm run dev
```

Vite 会把 `/api` 请求代理到该 backend。端口被占用或依赖服务未就绪时，应先处理现有进程或服务；仓库不提供强制终止未知进程的一键脚本。

其他脚本：

```bash
npm run build    # tsc -b + vite build
npm run lint     # eslint .
npm test         # vitest run
npm run e2e      # Playwright；默认指向 E2E_BASE_URL 或 http://localhost:5173
```

## 目录约定

```text
src/
├── api/          # 后端接口封装（fetch + SSE）
├── components/   # 组件：chat / message / sidebar / ui(shadcn)
├── hooks/        # useSSE 流式控制、useAuthedBlobUrl 等
├── stores/       # Zustand 状态
└── types/        # 共享类型
```

整体架构见仓库根目录 [ARCHITECTURE.md](../ARCHITECTURE.md)。
