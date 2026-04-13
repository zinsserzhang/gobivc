package api

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zinsserzhang/gobivc/internal/extractor"
	"github.com/zinsserzhang/gobivc/internal/model"
)

const (
	maxUploadSize = 50 << 20 // 50MB per file
	maxFiles      = 20
)

// UploadDir is the directory for storing uploaded files.
var UploadDir = "./data/uploads"

// uploadedFiles is an in-memory cache of uploaded files (keyed by file ID).
// In production, this should be in a database.
var uploadedFiles = make(map[string]*model.UploadedFile)

// HandleUpload handles POST /api/uploads (multipart form).
func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadSize * maxFiles); err != nil {
		errorResponse(w, http.StatusBadRequest, "文件过大或格式错误")
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		errorResponse(w, http.StatusBadRequest, "请选择至少一个文件")
		return
	}
	if len(files) > maxFiles {
		errorResponse(w, http.StatusBadRequest, fmt.Sprintf("最多上传 %d 个文件", maxFiles))
		return
	}

	// Ensure upload directory exists
	os.MkdirAll(UploadDir, 0755)

	var results []model.UploadedFile

	for _, fh := range files {
		// Validate file type
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if !isAllowedExt(ext) {
			log.Printf("WARNING: skipping unsupported file type: %s", fh.Filename)
			continue
		}

		// Generate unique ID
		id := generateFileID()
		savePath := filepath.Join(UploadDir, id+ext)

		// Save file to disk
		src, err := fh.Open()
		if err != nil {
			log.Printf("ERROR: open upload %s: %v", fh.Filename, err)
			continue
		}

		dst, err := os.Create(savePath)
		if err != nil {
			src.Close()
			log.Printf("ERROR: create file %s: %v", savePath, err)
			continue
		}

		written, err := io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			log.Printf("ERROR: save file %s: %v", fh.Filename, err)
			continue
		}

		// Extract text content
		text, err := extractor.ExtractText(savePath)
		if err != nil {
			log.Printf("WARNING: text extraction failed for %s: %v", fh.Filename, err)
			text = ""
		}

		// Truncate extracted text to ~8000 chars per file
		if len([]rune(text)) > 8000 {
			text = string([]rune(text)[:8000]) + "..."
		}

		uf := model.UploadedFile{
			ID:       id,
			Name:     fh.Filename,
			Size:     written,
			MimeType: fh.Header.Get("Content-Type"),
			Text:     text,
		}

		uploadedFiles[id] = &uf
		results = append(results, uf)

		log.Printf("INFO: uploaded %s (%d bytes, %d chars extracted)", fh.Filename, written, len([]rune(text)))
	}

	if len(results) == 0 {
		errorResponse(w, http.StatusBadRequest, "没有成功处理的文件，请检查文件格式")
		return
	}

	jsonResponse(w, http.StatusOK, results)
}

// GetUploadedFile retrieves an uploaded file by ID.
func GetUploadedFile(id string) *model.UploadedFile {
	return uploadedFiles[id]
}

func isAllowedExt(ext string) bool {
	allowed := map[string]bool{
		".pdf": true, ".docx": true, ".doc": true, ".pptx": true,
		".xlsx": true, ".xls": true,
		".txt": true, ".md": true, ".csv": true, ".json": true,
	}
	return allowed[ext]
}

func generateFileID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("f_%x", b)
}
