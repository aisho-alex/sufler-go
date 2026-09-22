package vad

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	HOP          = 512
	ContextSize  = 64
	EncoderFrame = HOP + ContextSize
)

var initOnce sync.Once

func LibraryPath() string {
	if p := os.Getenv("SUFLER_ONNXRUNTIME_LIB"); p != "" {
		return p
	}
	name := "onnxruntime.so"
	if runtime.GOOS == "windows" {
		name = "onnxruntime.dll"
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			p := filepath.Join(dir, "lib", name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return filepath.Join("lib", name)
}

func ModelsDir() string {
	if p := os.Getenv("SUFLER_MODELS"); p != "" {
		return p
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			p := filepath.Join(dir, "models")
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return p
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "models"
}

func Initialize() error {
	var err error
	initOnce.Do(func() {
		if ort.IsInitialized() {
			return
		}
		ort.SetSharedLibraryPath(LibraryPath())
		err = ort.InitializeEnvironment()
	})
	return err
}

type Model struct {
	encoder *ort.DynamicAdvancedSession
	decoder *ort.DynamicAdvancedSession
}

func NewModel(modelsDir string) (*Model, error) {
	if err := Initialize(); err != nil {
		return nil, fmt.Errorf("onnxruntime: %w", err)
	}
	encPath := filepath.Join(modelsDir, "silero_encoder_v5.onnx")
	decPath := filepath.Join(modelsDir, "silero_decoder_v5.onnx")

	encInputs, encOutputs, err := ort.GetInputOutputInfo(encPath)
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	decInputs, decOutputs, err := ort.GetInputOutputInfo(decPath)
	if err != nil {
		return nil, fmt.Errorf("decoder: %w", err)
	}

	encIn := names(encInputs)
	encOut := names(encOutputs)
	decIn := names(decInputs)
	decOut := names(decOutputs)

	encSession, err := ort.NewDynamicAdvancedSession(encPath, encIn, encOut, nil)
	if err != nil {
		return nil, fmt.Errorf("encoder session: %w", err)
	}
	decSession, err := ort.NewDynamicAdvancedSession(decPath, decIn, decOut, nil)
	if err != nil {
		encSession.Destroy()
		return nil, fmt.Errorf("decoder session: %w", err)
	}
	return &Model{encoder: encSession, decoder: decSession}, nil
}

func (m *Model) Destroy() {
	if m.encoder != nil {
		m.encoder.Destroy()
	}
	if m.decoder != nil {
		m.decoder.Destroy()
	}
}

func names(info []ort.InputOutputInfo) []string {
	out := make([]string, 0, len(info))
	for _, i := range info {
		out = append(out, i.Name)
	}
	return out
}

func (m *Model) Probs(audio []float32) ([]float32, error) {
	n := len(audio) / HOP
	if n == 0 {
		return nil, nil
	}

	encIn := make([]float32, n*EncoderFrame)
	copy(encIn[ContextSize:], audio[:n*HOP])
	for w := 1; w < n; w++ {
		copy(encIn[w*EncoderFrame:w*EncoderFrame+ContextSize],
			audio[(w-1)*HOP+HOP-ContextSize:w*HOP])
	}

	encInTensor, err := ort.NewTensor(
		ort.NewShape(int64(n), EncoderFrame), encIn)
	if err != nil {
		return nil, err
	}
	defer encInTensor.Destroy()

	var encOutVal [1]ort.Value
	if err := m.encoder.Run([]ort.Value{encInTensor}, encOutVal[:]); err != nil {
		return nil, fmt.Errorf("encoder run: %w", err)
	}
	defer encOutVal[0].Destroy()
	encOutT, ok := encOutVal[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("encoder: неожиданный тип выхода")
	}
	encOut := encOutT.GetData()
	if len(encOut) < n*128 {
		return nil, fmt.Errorf("encoder: короткий выход %d", len(encOut))
	}

	decIn := make([]float32, 128)
	state := make([]float32, 2*1*128)
	probs := make([]float32, n)

	decInTensor, err := ort.NewTensor(ort.NewShape(1, 128), decIn)
	if err != nil {
		return nil, err
	}
	defer decInTensor.Destroy()
	stateInTensor, err := ort.NewTensor(ort.NewShape(2, 1, 128), state)
	if err != nil {
		return nil, err
	}
	defer stateInTensor.Destroy()

	probOut, err := ort.NewTensor(ort.NewShape(1, 1, 1), make([]float32, 1))
	if err != nil {
		return nil, err
	}
	defer probOut.Destroy()
	stateOut, err := ort.NewTensor(ort.NewShape(2, 1, 128), make([]float32, 256))
	if err != nil {
		return nil, err
	}
	defer stateOut.Destroy()

	for w := 0; w < n; w++ {
		copy(decIn, encOut[w*128:(w+1)*128])
		if err := m.decoder.Run(
			[]ort.Value{decInTensor, stateInTensor},
			[]ort.Value{probOut, stateOut}); err != nil {
			return nil, fmt.Errorf("decoder run: %w", err)
		}
		probs[w] = probOut.GetData()[0]
		copy(state, stateOut.GetData())
	}
	return probs, nil
}
