import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def chunk(content: str, finish_reason=None, role=None) -> bytes:
    delta = {"content": content}
    if role:
        delta["role"] = role
    payload = {
        "id": "chatcmpl-effchat-e2e",
        "object": "chat.completion.chunk",
        "created": 1,
        "model": "effchat-e2e",
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
    }
    return f"data: {json.dumps(payload, ensure_ascii=False)}\n\n".encode()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_error(404)

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        body = json.loads(self.rfile.read(int(self.headers.get("content-length", "0"))) or b"{}")
        text = "\n".join(str(message.get("content", "")) for message in body.get("messages", []))
        if "Return strict JSON only:" in text and "one conversation-scoped memory card" in text:
            response = json.dumps({
                "action": "none",
                "content": "",
                "summary": "无需更新会话记忆。",
            }, ensure_ascii=False)
        elif "summarizing conversations into durable continuation context" in text:
            response = (
                "<analysis>已覆盖虚构测试对话的继续上下文。</analysis>"
                "<summary>1. 用户的主要诉求与意图：验证手动压缩与撤销。\n"
                "2. 关键概念与话题：确定性隔离测试。\n"
                "3. 关键信息与素材：无真实用户数据。\n"
                "4. 已解决的问题与进行中的事项：已生成压缩摘要。\n"
                "5. 待办事项：验证撤销恢复历史。\n"
                "6. 当前进展：压缩模型调用完成。\n"
                "7. 可选的下一步：撤销本次压缩。</summary>"
            )
        elif "E2E_STOP_AFTER_FIRST_DELTA" in text:
            response = "首包已经到达，后续内容必须由用户停止。"
        elif "compact chat titles" in text:
            response = "E2E deterministic title"
        else:
            response = "E2E deterministic assistant reply."

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        self.wfile.write(chunk(response, role="assistant"))
        self.wfile.flush()
        if "E2E_STOP_AFTER_FIRST_DELTA" in text:
            time.sleep(30)
        try:
            self.wfile.write(chunk("", "stop"))
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, format, *args):
        return


ThreadingHTTPServer(("0.0.0.0", 8091), Handler).serve_forever()
