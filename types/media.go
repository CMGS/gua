package types

// MediaType identifies the type of a media attachment.
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVoice MediaType = "voice"
	MediaTypeVideo MediaType = "video"
	MediaTypeFile  MediaType = "file"
)

// MediaFile is a downloaded media attachment with a local path.
type MediaFile struct {
	Path     string
	Type     MediaType
	FileName string // original filename (for file attachments)
}
