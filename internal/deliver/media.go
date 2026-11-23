package deliver

import (
	"path/filepath"
	"strings"
)

// mediaTypes is gnosh's own table of what a file is, by the end of its
// name. It is small on purpose: the kinds of thing an agent hands over. A
// browser is told the type and told not to guess (nosniff), so a video
// plays, an image and a PDF show, and a page opens as a page; anything
// that is not in the table is offered as a download.
//
// The table is gnosh's own, and not the system's (mime.TypeByExtension
// reads files of the machine it runs on), so that the same file is served
// the same way on every machine.
var mediaTypes = map[string]string{
	// Video.
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".ogv":  "video/ogg",
	".avi":  "video/x-msvideo",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",

	// Sound.
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".flac": "audio/flac",

	// Pictures.
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".svg":  "image/svg+xml",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".tif":  "image/tiff",
	".tiff": "image/tiff",

	// Pages and papers.
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".pdf":  "application/pdf",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".json": "application/json",
	".xml":  "application/xml",
	".wasm": "application/wasm",

	// Text, which a browser shows as it is.
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/plain; charset=utf-8",
	".tsv":  "text/plain; charset=utf-8",
	".yaml": "text/plain; charset=utf-8",
	".yml":  "text/plain; charset=utf-8",
	".toml": "text/plain; charset=utf-8",
	".srt":  "text/plain; charset=utf-8",
	".vtt":  "text/vtt; charset=utf-8",
	".go":   "text/plain; charset=utf-8",
	".gno":  "text/plain; charset=utf-8",
	".py":   "text/plain; charset=utf-8",
	".sh":   "text/plain; charset=utf-8",
	".diff": "text/plain; charset=utf-8",

	// Bundles, which are downloaded.
	".zip": "application/zip",
	".gz":  "application/gzip",
	".tgz": "application/gzip",
	".tar": "application/x-tar",
}

// mediaOther is what a file is called whose name is not in the table: a
// browser saves such a file and shows nothing of it.
const mediaOther = "application/octet-stream"

// mediaType returns the media type a file of that name is served as.
func mediaType(name string) string {
	if t, ok := mediaTypes[strings.ToLower(filepath.Ext(name))]; ok {
		return t
	}
	return mediaOther
}

// scripted reports whether a browser runs scripts in a file of this name
// when it shows it: a page, a picture drawn in SVG, an XML document.
func scripted(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm", ".xhtml", ".svg", ".svgz", ".xml", ".xsl":
		return true
	}
	return false
}
