package blossom

import (
	"mime"
	"strings"
)

var commonMimeExtensions = map[string]string{
	"application/json":                         ".json",
	"application/pdf":                          ".pdf",
	"application/vnd.android.package-archive": ".apk",
	"application/vnd.sqlite3":                  ".sqlite3",
	"application/xml":                          ".xml",
	"audio/aac":                                ".aac",
	"audio/flac":                               ".flac",
	"audio/midi":                               ".midi",
	"audio/mp3":                                ".mp3",
	"audio/mpeg":                               ".mp3",
	"audio/mp4":                                ".m4a",
	"audio/ogg":                                ".ogg",
	"audio/wav":                                ".wav",
	"audio/webm":                               ".weba",
	"audio/x-aiff":                             ".aiff",
	"audio/x-m4a":                              ".m4a",
	"image/avif":                               ".avif",
	"image/gif":                                ".gif",
	"image/jpeg":                               ".jpg",
	"image/png":                                ".png",
	"image/svg+xml":                            ".svg",
	"image/webp":                               ".webp",
	"text/css":                                 ".css",
	"text/csv":                                 ".csv",
	"text/html":                                ".html",
	"text/javascript":                          ".js",
	"text/markdown":                            ".md",
	"text/plain":                               ".txt",
	"text/xml":                                 ".xml",
	"video/mp2t":                               ".ts",
	"video/mp4":                                ".mp4",
	"video/ogg":                                ".ogv",
	"video/quicktime":                          ".mov",
	"video/webm":                               ".webm",
	"video/x-matroska":                         ".mkv",
}

var commonExtensionMimes = map[string]string{
	".aac":     "audio/aac",
	".aiff":    "audio/x-aiff",
	".apk":     "application/vnd.android.package-archive",
	".avif":    "image/avif",
	".css":     "text/css; charset=utf-8",
	".csv":     "text/csv; charset=utf-8",
	".flac":    "audio/flac",
	".gif":     "image/gif",
	".html":    "text/html; charset=utf-8",
	".jpeg":    "image/jpeg",
	".jpg":     "image/jpeg",
	".js":      "text/javascript; charset=utf-8",
	".json":    "application/json",
	".m4a":     "audio/mp4",
	".md":      "text/markdown; charset=utf-8",
	".midi":    "audio/midi",
	".mkv":     "video/x-matroska",
	".mov":     "video/quicktime",
	".mp3":     "audio/mpeg",
	".mp4":     "video/mp4",
	".oga":     "audio/ogg",
	".ogg":     "audio/ogg",
	".ogv":     "video/ogg",
	".pdf":     "application/pdf",
	".png":     "image/png",
	".sqlite3": "application/vnd.sqlite3",
	".svg":     "image/svg+xml",
	".ts":      "video/mp2t",
	".txt":     "text/plain; charset=utf-8",
	".wav":     "audio/wav",
	".weba":    "audio/webm",
	".webm":    "video/webm",
	".webp":    "image/webp",
	".xml":     "application/xml",
}

func normalizeMIMEType(mimetype string) string {
	if mimetype == "" {
		return ""
	}

	base, _, err := mime.ParseMediaType(mimetype)
	if err == nil {
		return strings.ToLower(base)
	}

	if idx := strings.IndexByte(mimetype, ';'); idx >= 0 {
		mimetype = mimetype[:idx]
	}

	return strings.ToLower(strings.TrimSpace(mimetype))
}

func GetExtension(mimetype string) string {
	mimetype = normalizeMIMEType(mimetype)
	if mimetype == "" {
		return ""
	}

	if ext, ok := commonMimeExtensions[mimetype]; ok {
		return ext
	}

	exts, _ := mime.ExtensionsByType(mimetype)
	if len(exts) > 0 {
		return exts[0]
	}

	return ""
}

func GetMIMEType(ext string) string {
	if ext == "" {
		return ""
	}

	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && ext[0] != '.' {
		ext = "." + ext
	}

	if mimetype, ok := commonExtensionMimes[ext]; ok {
		return mimetype
	}

	return mime.TypeByExtension(ext)
}
