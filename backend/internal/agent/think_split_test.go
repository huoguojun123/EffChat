package agent

import "testing"

// feedAll 把多段增量依次喂入，返回累计的 content / thinking（含 flush）。
func feedAll(chunks ...string) (string, string) {
	var s thinkSplitter
	var c, t string
	for _, ch := range chunks {
		cc, tt := s.feed(ch)
		c += cc
		t += tt
	}
	cf, tf := s.flush()
	return c + cf, t + tf
}

func TestThinkSplitter_NoThinkPassthrough(t *testing.T) {
	c, th := feedAll("Hello ", "world")
	if c != "Hello world" {
		t.Errorf("content = %q, want %q", c, "Hello world")
	}
	if th != "" {
		t.Errorf("thinking = %q, want empty", th)
	}
}

func TestThinkSplitter_InlineThinkSingleChunk(t *testing.T) {
	c, th := feedAll("<think>注：①家庭成员</think>最终答案")
	if c != "最终答案" {
		t.Errorf("content = %q, want %q", c, "最终答案")
	}
	if th != "注：①家庭成员" {
		t.Errorf("thinking = %q, want %q", th, "注：①家庭成员")
	}
}

func TestThinkSplitter_TagSpansChunks(t *testing.T) {
	// 开标签、思考、闭标签都被切在 chunk 边界上。
	c, th := feedAll("<thi", "nk>rea", "soning</thi", "nk>ans", "wer")
	if c != "answer" {
		t.Errorf("content = %q, want %q", c, "answer")
	}
	if th != "reasoning" {
		t.Errorf("thinking = %q, want %q", th, "reasoning")
	}
}

func TestThinkSplitter_LeadingWhitespaceBeforeThink(t *testing.T) {
	c, th := feedAll("  \n", "<think>r</think>a")
	if c != "a" {
		t.Errorf("content = %q, want %q", c, "a")
	}
	if th != "r" {
		t.Errorf("thinking = %q, want %q", th, "r")
	}
}

func TestThinkSplitter_OnlyThinkNoContent(t *testing.T) {
	c, th := feedAll("<think>just reasoning</think>")
	if c != "" {
		t.Errorf("content = %q, want empty", c)
	}
	if th != "just reasoning" {
		t.Errorf("thinking = %q, want %q", th, "just reasoning")
	}
}

func TestThinkSplitter_UnclosedThink(t *testing.T) {
	// 流断在 <think> 内部：已识别部分全部归思考，正文为空。
	c, th := feedAll("<think>partial reasoning never closed")
	if c != "" {
		t.Errorf("content = %q, want empty", c)
	}
	if th != "partial reasoning never closed" {
		t.Errorf("thinking = %q, want %q", th, "partial reasoning never closed")
	}
}

func TestThinkSplitter_ThinkNotAtStartIsContent(t *testing.T) {
	// 现代模型正文里合法出现的 <think> 字面（非流首）不应被吃掉。
	c, th := feedAll("Here is a tag: <think> in my answer")
	if c != "Here is a tag: <think> in my answer" {
		t.Errorf("content = %q, want verbatim", c)
	}
	if th != "" {
		t.Errorf("thinking = %q, want empty", th)
	}
}

func TestThinkSplitter_TextThatLooksLikePartialThenIsnt(t *testing.T) {
	// 流首以 "<th" 开头但最终长成 "<thanks>"，不是 <think> → 全部正文。
	c, th := feedAll("<th", "anks> for reading")
	if c != "<thanks> for reading" {
		t.Errorf("content = %q, want %q", c, "<thanks> for reading")
	}
	if th != "" {
		t.Errorf("thinking = %q, want empty", th)
	}
}

func TestSplitThinkContent_WholeString(t *testing.T) {
	c, th := splitThinkContent("<think>secret</think>visible")
	if c != "visible" || th != "secret" {
		t.Errorf("got content=%q thinking=%q, want content=visible thinking=secret", c, th)
	}

	// 无 think：原样返回。
	c2, th2 := splitThinkContent("plain answer")
	if c2 != "plain answer" || th2 != "" {
		t.Errorf("got content=%q thinking=%q, want content=plain answer thinking=empty", c2, th2)
	}
}
