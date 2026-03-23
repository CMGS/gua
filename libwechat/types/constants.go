package types

const (
	// Message types identify the sender role.
	MessageTypeNone = 0
	MessageTypeUser = 1
	MessageTypeBot  = 2

	// Message states track the lifecycle of a message.
	MessageStateNew        = 0
	MessageStateGenerating = 1
	MessageStateFinish     = 2

	// Item types identify the content type of a message item.
	ItemTypeNone  = 0
	ItemTypeText  = 1
	ItemTypeImage = 2
	ItemTypeVoice = 3
	ItemTypeFile  = 4
	ItemTypeVideo = 5

	// Typing status values for sendtyping API.
	TypingStatusTyping = 1
	TypingStatusCancel = 2

	// Upload media types for getuploadurl API.
	UploadMediaTypeImage = 1
	UploadMediaTypeVideo = 2
	UploadMediaTypeFile  = 3
	UploadMediaTypeVoice = 4

	// Encrypt types for CDN media.
	EncryptTypeAES128ECB = 1

	// Voice encode types.
	VoiceEncodeTypePCM      = 1
	VoiceEncodeTypeADPCM    = 2
	VoiceEncodeTypeFeature  = 3
	VoiceEncodeTypeSpeex    = 4
	VoiceEncodeTypeAMR      = 5
	VoiceEncodeTypeSILK     = 6
	VoiceEncodeTypeMP3      = 7
	VoiceEncodeTypeOggSpeex = 8

	// QR code login status values.
	QRStatusWait      = "wait"
	QRStatusScanned   = "scaned"
	QRStatusConfirmed = "confirmed"
	QRStatusExpired   = "expired"
)
