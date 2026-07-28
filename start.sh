#!/bin/bash

# EffChat 一键启动脚本
# 同时启动后端 (Go) 和前端 (Vite dev server)

set +e

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
BACKEND_DIR="$ROOT_DIR/backend"
FRONTEND_DIR="$ROOT_DIR/frontend"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

cleanup() {
  echo ""
  echo -e "${YELLOW}Shutting down...${NC}"
  if [ -n "$BACKEND_PID" ]; then
    kill "$BACKEND_PID" 2>/dev/null && echo -e "${GREEN}Backend stopped${NC}"
  fi
  if [ -n "$FRONTEND_PID" ]; then
    kill "$FRONTEND_PID" 2>/dev/null && echo -e "${GREEN}Frontend stopped${NC}"
  fi
  exit 0
}

trap cleanup SIGINT SIGTERM

# 检查依赖
if ! command -v go &>/dev/null; then
  echo -e "${RED}Error: go not found${NC}"
  exit 1
fi
if ! command -v node &>/dev/null; then
  echo -e "${RED}Error: node not found${NC}"
  exit 1
fi

# 清理残留端口
for port in 8080 5173; do
  pid=$(lsof -ti:$port 2>/dev/null)
  if [ -n "$pid" ]; then
    echo -e "${YELLOW}Killing process on port $port (PID: $pid)${NC}"
    kill -9 $pid 2>/dev/null
    sleep 0.5
  fi
done

# 检查 PostgreSQL (docker)
if docker ps 2>/dev/null | grep -qE 'postgres|pgvector|5432'; then
  echo -e "${GREEN}✓ PostgreSQL running${NC}"
else
  echo -e "${YELLOW}⚠ PostgreSQL container not detected, backend may fail to connect${NC}"
fi

# 启动后端
echo -e "${GREEN}Starting backend...${NC}"
cd "$BACKEND_DIR"
go run ./cmd/server &
BACKEND_PID=$!
cd "$ROOT_DIR"

# 等后端启动
sleep 2

# 启动前端
echo -e "${GREEN}Starting frontend...${NC}"
cd "$FRONTEND_DIR"
npx vite --host &
FRONTEND_PID=$!
cd "$ROOT_DIR"

echo ""
echo -e "${GREEN}═══════════════════════════════════════${NC}"
echo -e "${GREEN}  EffChat running${NC}"
echo -e "${GREEN}  Frontend: http://localhost:5173${NC}"
echo -e "${GREEN}  Backend:  http://localhost:8080${NC}"
echo -e "${GREEN}  Press Ctrl+C to stop${NC}"
echo -e "${GREEN}═══════════════════════════════════════${NC}"
echo ""

wait
