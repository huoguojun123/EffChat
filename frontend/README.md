# EffChat 前端

EffChat 的 Web 前端：轻量 AI 对话界面，流式输出、代码块工作区、多模型切换。

## 技术栈

- **构建**：Vite + React 19 + TypeScript
- **UI**：Shadcn/ui（Radix）+ Tailwind CSS v4
- **状态**：Zustand
- **动画**：GSAP + tailwindcss-animate
- **流式渲染**：react-markdown + remark（恒速打字机 + 淡入）

## 本地开发

推荐用仓库根目录的 `start.sh` 一键起前后端（Vite dev server + Go 后端）：

```bash
../start.sh
```

仅起前端：

```bash
npm install
npm run dev      # Vite dev server
```

其他脚本：

```bash
npm run build    # tsc -b + vite build
npm run lint     # eslint .
npm test         # vitest run
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
