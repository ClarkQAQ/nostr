package blossom

import "mime"

func GetExtension(mimetype string) string {
	if mimetype == "" {
		return ""
	}

	switch mimetype {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "application/vnd.android.package-archive":
		return ".apk"
	}

	exts, _ := mime.ExtensionsByType(mimetype)
	if len(exts) > 0 {
		return exts[0]
	}

	return ""
}
