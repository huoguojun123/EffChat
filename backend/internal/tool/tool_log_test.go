package tool

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func captureToolLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	return &buf
}

func requireToolLogContains(t *testing.T, logs string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(logs, fragment) {
			t.Fatalf("log missing %q\nlogs:\n%s", fragment, logs)
		}
	}
}
