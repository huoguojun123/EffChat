package handler

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func uploadTestPNG(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return content.Bytes()
}

func uploadTestPNGHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	content := make([]byte, 33)
	copy(content, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(content[8:12], 13)
	copy(content[12:16], "IHDR")
	binary.BigEndian.PutUint32(content[16:20], width)
	binary.BigEndian.PutUint32(content[20:24], height)
	content[24] = 8
	content[25] = 6
	binary.BigEndian.PutUint32(content[29:33], crc32.ChecksumIEEE(content[12:29]))
	return content
}

func TestValidateUploadImage(t *testing.T) {
	cases := []struct {
		name         string
		content      []byte
		declaredType string
		wantErr      string
	}{
		{"valid PNG", uploadTestPNG(t), "image/png", ""},
		{"mismatched type", uploadTestPNG(t), "image/jpeg", "does not match"},
		{"invalid image", []byte("not an image"), "image/png", "invalid or unsupported"},
		{"pixel limit", uploadTestPNGHeader(t, 8_001, 5_000), "image/png", "pixel limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actualType, err := validateUploadImage(tc.content, tc.declaredType)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("validateUploadImage() error = %v", err)
			}
			if tc.wantErr == "" && actualType != tc.declaredType {
				t.Fatalf("validateUploadImage() type = %q, want %q", actualType, tc.declaredType)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("validateUploadImage() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeUploadAllowedTypesReplacesLegacyImageWildcard(t *testing.T) {
	got := normalizeUploadAllowedTypes([]string{"image/*", "application/pdf"})
	want := []string{"image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeUploadAllowedTypes() = %v, want %v", got, want)
	}
}

// uploadMultipart 构造一个 /api/v1/files 上传请求，可指定 part 的 Content-Type（声明 MIME）。
func uploadMultipart(t *testing.T, token string, sessionID int64, filename, declaredType string, content []byte, fields ...map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("session_id", fmt.Sprintf("%d", sessionID)); err != nil {
		t.Fatalf("write session_id: %v", err)
	}
	for _, values := range fields {
		for key, value := range values {
			if err := mw.WriteField(key, value); err != nil {
				t.Fatalf("write %s: %v", key, err)
			}
		}
	}
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename)}
	if declaredType != "" {
		h["Content-Type"] = []string{declaredType}
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func createUploadTestSession(t *testing.T, env *testEnv) int64 {
	t.Helper()
	var id int64
	err := env.db.QueryRow(
		`INSERT INTO sessions (user_id, title, model_id, provider) VALUES ($1, 'upload test', 'gpt-4o-mini', 'openai') RETURNING id`,
		env.userID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create upload test session: %v", err)
	}
	return id
}

func TestUploadLimits_ReturnsConfiguredLimits(t *testing.T) {
	env := setupTestEnv(t)
	prev := map[string][]byte{}
	for _, key := range []string{"file_upload_max_size_mb", "file_upload_max_batch_count", "file_upload_max_session_files"} {
		var value []byte
		_ = env.db.QueryRow("SELECT value FROM system_config WHERE key=$1", key).Scan(&value)
		prev[key] = value
	}
	if _, err := env.db.Exec(`
		INSERT INTO system_config (key, value, config_type) VALUES
			('file_upload_max_size_mb', '12', 'number'),
			('file_upload_max_session_files', '9', 'number')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, config_type = EXCLUDED.config_type
	`); err != nil {
		t.Fatalf("seed upload limits: %v", err)
	}
	t.Cleanup(func() {
		for key, value := range prev {
			if value != nil {
				env.db.Exec("UPDATE system_config SET value = $1 WHERE key = $2", value, key)
			} else {
				env.db.Exec("DELETE FROM system_config WHERE key = $1", key)
			}
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/upload-limits", nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	var body struct {
		MaxFileSizeMB   int      `json:"max_file_size_mb"`
		MaxSessionFiles int      `json:"max_session_files"`
		AllowedTypes    []string `json:"allowed_types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal upload limits: %v", err)
	}
	if body.MaxFileSizeMB != 12 || body.MaxSessionFiles != 9 {
		t.Fatalf("unexpected limits: %+v", body)
	}
	if strings.Contains(w.Body.String(), "max_batch_count") {
		t.Fatalf("upload limits must not expose the retired upload-batch limit: %s", w.Body.String())
	}
	if len(body.AllowedTypes) == 0 {
		t.Fatal("allowed_types should be returned for frontend precheck")
	}
}

func TestUploadLimitsClampsLegacyPolicyToDeploymentCeiling(t *testing.T) {
	env := setupTestEnv(t)
	var previous []byte
	_ = env.db.QueryRow("SELECT value FROM system_config WHERE key = 'file_upload_max_size_mb'").Scan(&previous)
	if _, err := env.db.Exec("UPDATE system_config SET value = '50' WHERE key = 'file_upload_max_size_mb'"); err != nil {
		t.Fatalf("seed legacy upload size: %v", err)
	}
	t.Cleanup(func() {
		if previous != nil {
			_, _ = env.db.Exec("UPDATE system_config SET value = $1 WHERE key = 'file_upload_max_size_mb'", previous)
		}
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", env.userID)
		c.Next()
	})
	router.GET("/limits", UploadLimitsHandler(repository.NewConfigRepository(env.db), 25<<20))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/limits", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var body struct {
		MaxFileSizeMB int `json:"max_file_size_mb"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode limits: %v", err)
	}
	if body.MaxFileSizeMB != 25 {
		t.Fatalf("effective upload limit=%d, want 25", body.MaxFileSizeMB)
	}
}

func TestUploadPolicyEndpointsFailClosedWhenConfigurationIsUnavailable(t *testing.T) {
	db := setupHandlerTestDB(t)
	configRepo := repository.NewConfigRepository(db)
	if err := db.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "req-file-policy")
		c.Next()
	})
	router.GET("/limits", UploadLimitsHandler(configRepo, defaultDeploymentUploadMaxBytes))
	router.POST("/files", UploadFileHandler(nil, configRepo))

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/limits"},
		{method: http.MethodPost, path: "/files"},
	}
	for _, test := range tests {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status=%d, want %d (body: %s)", test.method, test.path, w.Code, http.StatusServiceUnavailable, w.Body.String())
		}
		var body struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s %s response: %v", test.method, test.path, err)
		}
		if body.Code != "file_policy_unavailable" {
			t.Fatalf("%s %s code=%q, want file_policy_unavailable", test.method, test.path, body.Code)
		}
		if !body.Retryable || body.RequestID != "req-file-policy" {
			t.Fatalf("%s %s response=%+v, want retryable request correlation", test.method, test.path, body)
		}
	}
}

func TestFilePoliciesKeepLastStrictValuesAfterParseFailure(t *testing.T) {
	db := setupHandlerTestDB(t)
	defer db.Close()
	configRepo := repository.NewConfigRepository(db)
	if _, err := db.Exec(`
		UPDATE system_config SET value = '1' WHERE key IN ('file_upload_max_size_mb', 'file_upload_max_session_files');
		UPDATE system_config SET value = '["text/plain"]' WHERE key = 'file_upload_allowed_types';
		UPDATE system_config SET value = 'false' WHERE key = 'attachment_extract_enabled';
		UPDATE system_config SET value = '30' WHERE key = 'attachment_extract_timeout_seconds';
		UPDATE system_config SET value = '1' WHERE key = 'attachment_max_output_mb';
	`); err != nil {
		t.Fatalf("seed strict file policies: %v", err)
	}

	limits, err := resolveUploadLimits(t.Context(), configRepo, defaultDeploymentUploadMaxBytes)
	if err != nil || limits.PolicyDegraded || limits.MaxFileSizeMB != 1 || limits.MaxSessionFiles != 1 || !reflect.DeepEqual(limits.AllowedTypes, []string{"text/plain"}) {
		t.Fatalf("prime upload policy: limits=%+v err=%v", limits, err)
	}
	attachment, err := resolveAttachmentProcessingPolicy(t.Context(), configRepo)
	if err != nil || attachment.Degraded || attachment.Enabled || attachment.TimeoutSeconds != 30 || attachment.MaxOutputMB != 1 {
		t.Fatalf("prime attachment policy: policy=%+v err=%v", attachment, err)
	}

	if _, err := db.Exec(`
		UPDATE system_config SET value = '"invalid"' WHERE key IN ('file_upload_max_size_mb', 'file_upload_max_session_files', 'attachment_extract_timeout_seconds', 'attachment_max_output_mb');
		UPDATE system_config SET value = '{}' WHERE key = 'file_upload_allowed_types';
		UPDATE system_config SET value = '"invalid"' WHERE key = 'attachment_extract_enabled';
	`); err != nil {
		t.Fatalf("inject malformed file policies: %v", err)
	}

	limits, err = resolveUploadLimits(t.Context(), configRepo, defaultDeploymentUploadMaxBytes)
	if err != nil || !limits.PolicyDegraded || limits.MaxFileSizeMB != 1 || limits.MaxSessionFiles != 1 || !reflect.DeepEqual(limits.AllowedTypes, []string{"text/plain"}) {
		t.Fatalf("degraded upload policy widened: limits=%+v err=%v", limits, err)
	}
	attachment, err = resolveAttachmentProcessingPolicy(t.Context(), configRepo)
	if err != nil || !attachment.Degraded || attachment.Enabled || attachment.TimeoutSeconds != 30 || attachment.MaxOutputMB != 1 {
		t.Fatalf("degraded attachment policy widened: policy=%+v err=%v", attachment, err)
	}
}

func TestUploadFailsClosedWhenAttachmentPolicyIsMalformed(t *testing.T) {
	env := setupTestEnv(t)
	var previous []byte
	_ = env.db.QueryRow("SELECT value FROM system_config WHERE key = 'attachment_extract_enabled'").Scan(&previous)
	if _, err := env.db.Exec(`
		INSERT INTO system_config (key, value, config_type)
		VALUES ('attachment_extract_enabled', '"invalid"', 'boolean')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`); err != nil {
		t.Fatalf("seed malformed attachment policy: %v", err)
	}
	t.Cleanup(func() {
		if previous == nil {
			_, _ = env.db.Exec("DELETE FROM system_config WHERE key = 'attachment_extract_enabled'")
			return
		}
		_, _ = env.db.Exec("UPDATE system_config SET value = $1 WHERE key = 'attachment_extract_enabled'", previous)
	})

	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, uploadMultipart(t, env.token, 1, "note.txt", "text/plain", []byte("test content")))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d (body: %s)", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "attachment_policy_unavailable" {
		t.Fatalf("code=%q, want attachment_policy_unavailable", body.Code)
	}
}

func TestUpload_DoesNotTreatSelectionSizeAsAnUploadLimit(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
	})

	req := uploadMultipart(t, env.token, sessionID, "screenshot.png", "image/png", uploadTestPNG(t), map[string]string{"batch_count": "24"})
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status=%d, want %d (body: %s)", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
}

func TestUploadImageUsesPrivateManagedAttachmentModes(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	t.Cleanup(func() {
		_, _ = env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
	})

	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, uploadMultipart(t, env.token, sessionID, "private.png", "image/png", uploadTestPNG(t)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status=%d, want %d (body: %s)", recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	var filePath string
	if err := env.db.QueryRow("SELECT file_path FROM files WHERE user_id = $1 ORDER BY id DESC LIMIT 1", env.userID).Scan(&filePath); err != nil {
		t.Fatalf("query uploaded path: %v", err)
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat uploaded file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("uploaded file mode=%#o, want 0600", got)
	}
	monthDir := filepath.Dir(filePath)
	for _, dir := range []string{monthDir, filepath.Dir(monthDir), filepath.Dir(filepath.Dir(monthDir))} {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatalf("stat attachment directory %s: %v", dir, statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("attachment directory %s mode=%#o, want 0700", dir, got)
		}
	}
}

func TestUpload_RejectsOversizedMultipartBody(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	var prevSize []byte
	_ = env.db.QueryRow("SELECT value FROM system_config WHERE key='file_upload_max_size_mb'").Scan(&prevSize)
	if _, err := env.db.Exec(
		"INSERT INTO system_config (key, value, config_type) VALUES ('file_upload_max_size_mb', '1', 'number') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
	); err != nil {
		t.Fatalf("seed upload size limit: %v", err)
	}
	t.Cleanup(func() {
		if prevSize != nil {
			env.db.Exec("UPDATE system_config SET value = $1 WHERE key='file_upload_max_size_mb'", prevSize)
		}
		env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprintf("%d", env.userID)))
	})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	filePart, err := mw.CreateFormFile("file", "tiny.txt")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := filePart.Write([]byte("tiny")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	bigPart, err := mw.CreateFormField("padding")
	if err != nil {
		t.Fatalf("create padding part: %v", err)
	}
	if _, err := bigPart.Write(bytes.Repeat([]byte("a"), 3<<20)); err != nil {
		t.Fatalf("write padding part: %v", err)
	}
	if err := mw.WriteField("session_id", fmt.Sprintf("%d", sessionID)); err != nil {
		t.Fatalf("write session_id: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.token)

	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want %d (body: %s)", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
	body := decodeUploadError(t, w)
	if body.Code != "file_too_large" || body.Retryable {
		t.Fatalf("oversized response = %+v", body)
	}
}

// 上传白名单必须按真实内容嗅探，不能只信 multipart 头：伪装成 text/plain 的二进制应被拒，
// 真正的文本/json 应放行。这守护 P1-1（真实 MIME 校验）与 P0-2（声称支持的类型可上传）。
func TestUpload_RejectsDisguisedBinaryAcceptsRealText(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	// 把白名单固定为权威默认值，使断言不依赖共享开发库的环境配置；结束后还原。
	var prevAllowed []byte
	_ = env.db.QueryRow("SELECT value FROM system_config WHERE key='file_upload_allowed_types'").Scan(&prevAllowed)
	var prevCount []byte
	_ = env.db.QueryRow("SELECT value FROM system_config WHERE key='file_upload_max_session_files'").Scan(&prevCount)
	if _, err := env.db.Exec(
		"INSERT INTO system_config (key, value, config_type) VALUES ('file_upload_allowed_types', $1, 'json') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
		`["image/*","application/pdf","text/*","application/json","application/xml","application/vnd.openxmlformats-officedocument.wordprocessingml.document","application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"]`,
	); err != nil {
		t.Fatalf("seed allowed types: %v", err)
	}
	if _, err := env.db.Exec(
		"INSERT INTO system_config (key, value, config_type) VALUES ('file_upload_max_session_files', '20', 'number') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, config_type = EXCLUDED.config_type",
	); err != nil {
		t.Fatalf("seed session file count limit: %v", err)
	}
	t.Cleanup(func() {
		if prevAllowed != nil {
			env.db.Exec("UPDATE system_config SET value = $1 WHERE key='file_upload_allowed_types'", prevAllowed)
		} else {
			env.db.Exec("DELETE FROM system_config WHERE key='file_upload_allowed_types'")
		}
		if prevCount != nil {
			env.db.Exec("UPDATE system_config SET value = $1 WHERE key='file_upload_max_session_files'", prevCount)
		} else {
			env.db.Exec("DELETE FROM system_config WHERE key='file_upload_max_session_files'")
		}
		env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprintf("%d", env.userID)))
	})

	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}

	cases := []struct {
		name         string
		filename     string
		declaredType string
		content      []byte
		wantStatus   int
		wantCode     string
	}{
		{"binary disguised as text rejected", "evil.txt", "text/plain", pngBytes, http.StatusBadRequest, "file_type_mismatch"},
		{"binary disguised as json rejected", "evil.json", "application/json", pngBytes, http.StatusBadRequest, "file_type_mismatch"},
		{"real text accepted", "note.txt", "text/plain", []byte("hello world\nplain text body"), http.StatusCreated, ""},
		{"real json accepted", "data.json", "application/json", []byte(`{"k":"v","n":1}`), http.StatusCreated, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			env.router.ServeHTTP(w, uploadMultipart(t, env.token, sessionID, c.filename, c.declaredType, c.content))
			if w.Code != c.wantStatus {
				t.Errorf("status=%d, want %d (body: %s)", w.Code, c.wantStatus, w.Body.String())
			}
			if c.wantCode != "" {
				body := decodeUploadError(t, w)
				if body.Code != c.wantCode || body.Retryable {
					t.Errorf("error response = %+v, want code %q", body, c.wantCode)
				}
			}
		})
	}
}

func TestUpload_ValidatesImageContentAgainstDeclaredType(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
	})

	pngContent := uploadTestPNG(t)
	cases := []struct {
		name         string
		filename     string
		declaredType string
		content      []byte
		wantStatus   int
		wantCode     string
	}{
		{"valid PNG accepted", "photo.png", "image/png", pngContent, http.StatusCreated, ""},
		{"PNG declared as JPEG rejected", "photo.jpg", "image/jpeg", pngContent, http.StatusBadRequest, "file_type_mismatch"},
		{"non-image declared as PNG rejected", "fake.png", "image/png", []byte("not an image"), http.StatusUnprocessableEntity, "image_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			env.router.ServeHTTP(w, uploadMultipart(t, env.token, sessionID, tc.filename, tc.declaredType, tc.content))
			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				body := decodeUploadError(t, w)
				if body.Code != tc.wantCode || body.Retryable {
					t.Fatalf("error response = %+v, want code %q", body, tc.wantCode)
				}
			}
		})
	}
}

func TestUpload_SanitizesFilenameAndStaysInUserDir(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprintf("%d", env.userID)))
		_ = os.Remove(filepath.Join(filepolicy.StorageRoot, "escape.txt"))
	})

	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, uploadMultipart(t, env.token, sessionID, "..\\..\\escape.txt", "text/plain", []byte("safe text")))
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d (body: %s)", w.Code, http.StatusCreated, w.Body.String())
	}

	var filePath, fileName string
	var extractedTextPath *string
	if err := env.db.QueryRow(
		"SELECT file_path, file_name, extracted_text_path FROM files WHERE user_id = $1 ORDER BY id DESC LIMIT 1",
		env.userID,
	).Scan(&filePath, &fileName, &extractedTextPath); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}

	if fileName != "escape.txt" {
		t.Fatalf("file_name=%q, want sanitized basename", fileName)
	}

	if extractedTextPath == nil || *extractedTextPath != filePath {
		t.Fatalf("extracted_text_path=%v, want same path as file_path %s", extractedTextPath, filePath)
	}

	extractedDirAbs, err := filepath.Abs(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprintf("%d", env.userID)))
	if err != nil {
		t.Fatalf("abs extracted dir: %v", err)
	}
	fileAbs, err := filepath.Abs(filePath)
	if err != nil {
		t.Fatalf("abs file path: %v", err)
	}
	rel, err := filepath.Rel(extractedDirAbs, fileAbs)
	if err != nil {
		t.Fatalf("rel file path: %v", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("extracted file escaped user dir: extractedDir=%s filePath=%s rel=%s", extractedDirAbs, fileAbs, rel)
	}
	if !strings.HasPrefix(rel, time.Now().Format("2006-01")+string(filepath.Separator)) {
		t.Fatalf("extracted file path should be sharded by month, rel=%s", rel)
	}
	if _, err := os.Stat(filepath.Join(filepolicy.StorageRoot, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("unexpected file outside user dir: %v", err)
	}
}

func TestSanitizeUploadFilenameRemovesUnicodeControlCharacters(t *testing.T) {
	got := sanitizeUploadFilename("  report\u202e\n.txt\x00  ")
	if got != "report.txt" {
		t.Fatalf("sanitizeUploadFilename removed controls = %q, want report.txt", got)
	}
	if got := sanitizeUploadFilename("\u202e\n\x00"); got != "file" {
		t.Fatalf("sanitizeUploadFilename controls-only = %q, want file", got)
	}
}

func TestUpload_RejectsWhenSessionFileCountExceeded(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	var prevCount []byte
	_ = env.db.QueryRow("SELECT value FROM system_config WHERE key='file_upload_max_session_files'").Scan(&prevCount)
	if _, err := env.db.Exec(
		"INSERT INTO system_config (key, value, config_type) VALUES ('file_upload_max_session_files', '1', 'number') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, config_type = EXCLUDED.config_type",
	); err != nil {
		t.Fatalf("seed session file count limit: %v", err)
	}
	t.Cleanup(func() {
		if prevCount != nil {
			env.db.Exec("UPDATE system_config SET value = $1 WHERE key='file_upload_max_session_files'", prevCount)
		} else {
			env.db.Exec("DELETE FROM system_config WHERE key='file_upload_max_session_files'")
		}
		env.db.Exec("DELETE FROM files WHERE user_id = $1", env.userID)
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", env.userID)))
		_ = os.RemoveAll(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprintf("%d", env.userID)))
	})

	first := httptest.NewRecorder()
	env.router.ServeHTTP(first, uploadMultipart(t, env.token, sessionID, "a.txt", "text/plain", []byte("first text")))
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}
	second := httptest.NewRecorder()
	env.router.ServeHTTP(second, uploadMultipart(t, env.token, sessionID, "b.txt", "text/plain", []byte("second text")))
	body := decodeUploadError(t, second)
	if second.Code != http.StatusConflict || body.Code != "session_file_limit_reached" || body.Retryable || !strings.Contains(body.Error, "too many active files") {
		t.Fatalf("second status/body=%d %s, want session count rejection", second.Code, second.Body.String())
	}
}
