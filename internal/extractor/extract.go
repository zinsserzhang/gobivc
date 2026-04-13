package extractor

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ExtractText reads a file and returns its text content.
// Supports: .txt, .md, .csv, .pdf, .docx, .pptx
func ExtractText(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".txt", ".md", ".csv", ".json":
		return readPlainText(filePath)
	case ".pdf":
		return extractPDF(filePath)
	case ".docx":
		return extractDOCX(filePath)
	case ".pptx":
		return extractPPTX(filePath)
	case ".doc":
		return extractDOCLegacy(filePath)
	default:
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}
}

func readPlainText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// extractPDF uses pdftotext (poppler-utils) to extract text from PDF.
func extractPDF(path string) (string, error) {
	out, err := exec.Command("pdftotext", "-layout", "-enc", "UTF-8", path, "-").Output()
	if err != nil {
		// Fallback: try with simpler flags
		out, err = exec.Command("pdftotext", path, "-").Output()
		if err != nil {
			return "", fmt.Errorf("pdftotext failed (is poppler-utils installed?): %w", err)
		}
	}
	return string(out), nil
}

// extractDOCX extracts text from .docx (Office Open XML) files.
func extractDOCX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}
			return stripXML(string(data)), nil
		}
	}
	return "", fmt.Errorf("word/document.xml not found in docx")
}

// extractPPTX extracts text from .pptx files.
func extractPPTX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	var buf bytes.Buffer
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			text := stripXML(string(data))
			if text != "" {
				buf.WriteString(text)
				buf.WriteString("\n\n")
			}
		}
	}
	return buf.String(), nil
}

// extractDOCLegacy tries antiword or catdoc for old .doc files.
func extractDOCLegacy(path string) (string, error) {
	// Try antiword first
	if out, err := exec.Command("antiword", path).Output(); err == nil {
		return string(out), nil
	}
	// Try catdoc
	if out, err := exec.Command("catdoc", path).Output(); err == nil {
		return string(out), nil
	}
	return "", fmt.Errorf("cannot extract .doc (install antiword or catdoc)")
}

// stripXML removes XML tags and returns plain text.
var xmlTagRegex = regexp.MustCompile(`<[^>]+>`)

func stripXML(s string) string {
	// Replace paragraph-like tags with newlines
	s = strings.ReplaceAll(s, "</w:p>", "\n")
	s = strings.ReplaceAll(s, "</a:p>", "\n")
	// Remove all XML tags
	s = xmlTagRegex.ReplaceAllString(s, "")
	// Clean up whitespace
	lines := strings.Split(s, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
