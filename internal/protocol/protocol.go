package protocol

type Kind string

const (
	KindText  Kind = "text"
	KindImage Kind = "image"
)

type CaptureEnvelope struct {
	Kind           Kind   `json:"kind"`
	Text           string `json:"text,omitempty"`
	ImagePNGBase64 string `json:"image_png_base64,omitempty"`
}

type TaskRequest struct {
	RequestID  string `json:"request_id"`
	InputPath  string `json:"input_path,omitempty"`
	ResultPath string `json:"result_path"`
}

type CaptureTaskResult struct {
	RequestID string           `json:"request_id"`
	OK        bool             `json:"ok"`
	Error     string           `json:"error,omitempty"`
	Capture   *CaptureEnvelope `json:"capture,omitempty"`
}

type SetClipboardTaskResult struct {
	RequestID string `json:"request_id"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}
