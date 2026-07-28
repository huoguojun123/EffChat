package agent

import "strings"

// 部分老模型（无独立 reasoning_content 字段）把思考链以内联 <think>...</think>
// 写进正文流。thinkSplitter 把这种内联思考从正文中切出来，路由到 thinking 通道，
// 使其进入折叠面板而非污染答案。
//
// 设计要点：
//   - 有状态、可分段喂（标签可能跨 chunk）；
//   - 仅识别「位于流首」的单个 <think> 块（忽略前导空白）。一旦流首出现的不是
//     <think>，立即转入 passthrough，永不再切——避免吃掉现代模型正文里合法出现的
//     "<think>" 字面（如代码、讨论标签本身）；
//   - 只认小写无空格的 <think> / </think>，不做模糊匹配。
const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

const (
	thinkModeUnknown     = iota // 流首未定：在等是否以 <think> 开头
	thinkModeThinking           // 已进入 <think>，正文前的思考段
	thinkModePassthrough        // 之后全部为正文
)

type thinkSplitter struct {
	mode    int
	pending string // 跨 chunk 的暂存：可能的部分标签或未决前导空白
}

// feed 处理一段正文增量，返回应作为正文 / 思考分别输出的片段。
func (s *thinkSplitter) feed(delta string) (content, thinking string) {
	work := s.pending + delta
	s.pending = ""
	var cb, tb strings.Builder

	for len(work) > 0 {
		switch s.mode {
		case thinkModeUnknown:
			trimmed := strings.TrimLeft(work, " \t\r\n")
			if trimmed == "" {
				// 目前只有空白，无法判定，暂存等下一段（若最终是正文则原样吐出）。
				s.pending = work
				work = ""
				continue
			}
			if strings.HasPrefix(trimmed, thinkOpen) {
				work = trimmed[len(thinkOpen):]
				s.mode = thinkModeThinking
				continue
			}
			if isProperPrefix(trimmed, thinkOpen) {
				// 还可能长成 <think>，整段暂存继续等。
				s.pending = work
				work = ""
				continue
			}
			// 流首不是 <think>：从此全部按正文透传。
			s.mode = thinkModePassthrough
		case thinkModeThinking:
			if idx := strings.Index(work, thinkClose); idx >= 0 {
				tb.WriteString(work[:idx])
				work = work[idx+len(thinkClose):]
				s.mode = thinkModePassthrough
				continue
			}
			keep := partialTagSuffix(work, thinkClose)
			tb.WriteString(work[:len(work)-keep])
			s.pending = work[len(work)-keep:]
			work = ""
		case thinkModePassthrough:
			cb.WriteString(work)
			work = ""
		}
	}
	return cb.String(), tb.String()
}

// flush 在流结束时吐出残留暂存：未闭合的 <think> 余下文本归入思考；
// 其余（未决的纯前导空白）归入正文。
func (s *thinkSplitter) flush() (content, thinking string) {
	if s.pending == "" {
		return "", ""
	}
	rem := s.pending
	s.pending = ""
	if s.mode == thinkModeThinking {
		return "", rem
	}
	return rem, ""
}

// splitThinkContent 对一段完整文本做一次性切割，供落库前清洗整条 content 复用。
func splitThinkContent(full string) (content, thinking string) {
	var s thinkSplitter
	c, t := s.feed(full)
	cf, tf := s.flush()
	return c + cf, t + tf
}

// isProperPrefix 判断 s 是否为 full 的真前缀（更短且逐字符相等）。
func isProperPrefix(s, full string) bool {
	return len(s) < len(full) && full[:len(s)] == s
}

// partialTagSuffix 返回 text 末尾「恰是 tag 某个真前缀」的最长长度，
// 用于把跨 chunk 的半截标签留到下一段，避免提前吐出。
func partialTagSuffix(text, tag string) int {
	max := len(tag) - 1
	if max > len(text) {
		max = len(text)
	}
	for k := max; k > 0; k-- {
		if text[len(text)-k:] == tag[:k] {
			return k
		}
	}
	return 0
}
