package service

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var ErrInvalidPreviewCursor = errors.New("invalid preview cursor")

type FilePreviewChunk struct {
	Content    string
	NextCursor string
	HasMore    bool
}

func ReadFilePreviewChunk(path, cursor string, maxChars int) (*FilePreviewChunk, error) {
	offset, err := decodePreviewCursor(cursor)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if offset > info.Size() {
		return nil, ErrInvalidPreviewCursor
	}
	if offset > 0 && offset < info.Size() {
		var boundary [1]byte
		if _, err := file.ReadAt(boundary[:], offset); err != nil {
			return nil, err
		}
		if boundary[0]&0xc0 == 0x80 {
			return nil, ErrInvalidPreviewCursor
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(file)
	var content strings.Builder
	consumed := int64(0)
	for i := 0; i < maxChars; i++ {
		r, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		content.WriteRune(r)
		consumed += int64(size)
	}
	_, peekErr := reader.Peek(1)
	hasMore := peekErr == nil
	if peekErr != nil && !errors.Is(peekErr, io.EOF) {
		return nil, peekErr
	}
	nextCursor := ""
	if hasMore {
		nextCursor = encodePreviewCursor(offset + consumed)
	}
	return &FilePreviewChunk{Content: content.String(), NextCursor: nextCursor, HasMore: hasMore}, nil
}

func encodePreviewCursor(offset int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("v1:%d", offset)))
}

func decodePreviewCursor(cursor string) (int64, error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalidPreviewCursor
	}
	prefix, rawOffset, ok := strings.Cut(string(decoded), ":")
	if !ok || prefix != "v1" {
		return 0, ErrInvalidPreviewCursor
	}
	offset, err := strconv.ParseInt(rawOffset, 10, 64)
	if err != nil || offset < 0 {
		return 0, ErrInvalidPreviewCursor
	}
	return offset, nil
}
