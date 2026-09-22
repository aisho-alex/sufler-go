//go:build windows

package capture

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// WASAPI захват в shared event-driven режиме: WASAPI сам приводит mix
// format устройства к 48 кГц моно int16 (AUTOCONVERTPCM), дальше decimator
// режет до 16 кГц. Для системного звука — loopback на дефолтном рендере
// (по схеме из go-wca: парный render-клиент нужен, чтобы event жил).

const (
	captureRate = 48000
	flagSilent  = 0x2 // AUDCLNT_BUFFERFLAGS_SILENT
)

type wasapiSource struct {
	label      string
	samplerate int
	blockMs    int

	mu       sync.Mutex
	started  bool
	stopCh   chan struct{}
	dead     chan struct{}
	deviceID string
}

func newWasapiSource(label string, samplerate, blockMs int) *wasapiSource {
	return &wasapiSource{label: label, samplerate: samplerate, blockMs: blockMs}
}

func (s *wasapiSource) Start(onAudio OnAudioFunc) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.stopCh = make(chan struct{})
	s.dead = make(chan struct{})
	stopCh, dead := s.stopCh, s.dead
	s.mu.Unlock()

	go func() {
		defer close(dead)
		s.run(stopCh, onAudio)
	}()
	return nil
}

func (s *wasapiSource) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	s.started = false
	close(s.stopCh)
	<-s.dead
}

func (s *wasapiSource) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func (s *wasapiSource) Name() string {
	if s.label == "monitor" {
		return "wasapi:loopback"
	}
	return "wasapi:mic"
}

func (s *wasapiSource) DefaultDeviceID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceID
}

func comInit() (ok bool, uninit func()) {
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return false, func() {}
	}
	return true, ole.CoUninitialize
}

func defaultEndpointID(label string) (string, error) {
	ok, uninit := comInit()
	if !ok {
		return "", fmt.Errorf("CoInitializeEx failed")
	}
	defer uninit()

	var mde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0,
		wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mde); err != nil {
		return "", err
	}
	defer mde.Release()

	flow := uint32(wca.ECapture)
	if label == "monitor" {
		flow = uint32(wca.ERender)
	}
	var mmd *wca.IMMDevice
	if err := mde.GetDefaultAudioEndpoint(flow, wca.EConsole, &mmd); err != nil {
		return "", err
	}
	defer mmd.Release()
	var id string
	if err := mmd.GetId(&id); err != nil {
		return "", err
	}
	return id, nil
}

func deviceFriendlyName(dev *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return ""
	}
	defer ps.Release()
	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return ""
	}
	return pv.String()
}

func wasapiDeviceNames() []string {
	ok, uninit := comInit()
	if !ok {
		return nil
	}
	defer uninit()

	var mde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0,
		wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mde); err != nil {
		return nil
	}
	defer mde.Release()

	var out []string
	for _, kind := range []struct {
		flow uint32
		tag  string
	}{{uint32(wca.ECapture), "микрофон"}, {uint32(wca.ERender), "система"}} {
		var coll *wca.IMMDeviceCollection
		if err := mde.EnumAudioEndpoints(kind.flow, 1, &coll); err != nil {
			continue
		}
		var count uint32
		coll.GetCount(&count)
		for i := uint32(0); i < count; i++ {
			var dev *wca.IMMDevice
			if err := coll.Item(i, &dev); err != nil {
				continue
			}
			name := deviceFriendlyName(dev)
			out = append(out, fmt.Sprintf("  [%s] %s", kind.tag, name))
			dev.Release()
		}
		coll.Release()
	}
	return out
}

func (s *wasapiSource) run(stopCh chan struct{}, onAudio OnAudioFunc) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ok, uninit := comInit()
	if !ok {
		return
	}
	defer uninit()

	if id, err := defaultEndpointID(s.label); err == nil {
		s.mu.Lock()
		s.deviceID = id
		s.mu.Unlock()
	}

	var mde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0,
		wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mde); err != nil {
		return
	}
	defer mde.Release()

	monitor := s.label == "monitor"
	flow := uint32(wca.ECapture)
	if monitor {
		flow = uint32(wca.ERender)
	}
	var mmd *wca.IMMDevice
	if err := mde.GetDefaultAudioEndpoint(flow, wca.EConsole, &mmd); err != nil {
		return
	}
	defer mmd.Release()

	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return
	}
	defer ac.Release()

	wfx := &wca.WAVEFORMATEX{
		WFormatTag:      1,
		NChannels:       1,
		NSamplesPerSec:  captureRate,
		WBitsPerSample:  16,
		NBlockAlign:     2,
		NAvgBytesPerSec: captureRate * 2,
	}
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK |
		wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM |
		wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	loopFlags := flags | wca.AUDCLNT_STREAMFLAGS_LOOPBACK

	var defaultPeriod wca.REFERENCE_TIME
	var minPeriod wca.REFERENCE_TIME
	_ = ac.GetDevicePeriod(&defaultPeriod, &minPeriod)

	if err := ac.Initialize(uint32(wca.AUDCLNT_SHAREMODE_SHARED), loopFlags,
		defaultPeriod, 0, wfx, nil); err != nil {
		return
	}

	audioReady := wca.CreateEventExA(0, 0, 0,
		wca.EVENT_MODIFY_STATE|wca.SYNCHRONIZE)
	defer wca.CloseHandle(audioReady)
	if err := ac.SetEventHandle(audioReady); err != nil {
		return
	}

	var rac *wca.IAudioClient
	if monitor {
		if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &rac); err != nil {
			return
		}
		defer rac.Release()
		if err := rac.Initialize(uint32(wca.AUDCLNT_SHAREMODE_SHARED), flags,
			defaultPeriod, 0, wfx, nil); err != nil {
			return
		}
		renderReady := wca.CreateEventExA(0, 0, 0,
			wca.EVENT_MODIFY_STATE|wca.SYNCHRONIZE)
		defer wca.CloseHandle(renderReady)
		if err := rac.SetEventHandle(renderReady); err != nil {
			return
		}
		if err := rac.Start(); err != nil {
			return
		}
		defer rac.Stop()
	}

	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		return
	}
	defer acc.Release()

	if err := ac.Start(); err != nil {
		return
	}
	defer ac.Stop()

	inBytesPerBlock := captureRate * s.blockMs / 1000 * 2
	var pending []byte

	for {
		select {
		case <-stopCh:
			return
		default:
		}
		if wca.WaitForSingleObject(audioReady, 200) != 0 {
			continue
		}
		for {
			var data *byte
			var frames, devFlags uint32
			var devPos, qcpPos uint64
			if err := acc.GetBuffer(&data, &frames, &devFlags,
				&devPos, &qcpPos); err != nil {
				break
			}
			if frames == 0 {
				break
			}
			lim := int(frames) * int(wfx.NBlockAlign)
			raw := make([]byte, lim)
			if devFlags&flagSilent == 0 && data != nil {
				copy(raw, unsafe.Slice((*byte)(unsafe.Pointer(data)), lim))
			}
			if err := acc.ReleaseBuffer(frames); err != nil {
				return
			}
			pending = append(pending, raw...)
			for len(pending) >= inBytesPerBlock {
				block := decimate48to16(pending[:inBytesPerBlock])
				pending = pending[inBytesPerBlock:]
				onAudio(s.label, block)
			}
		}
	}
}

func (s *AudioStream) resolve() source {
	return newWasapiSource(s.Label, s.Samplerate, s.BlockMs)
}

func (s *AudioStream) defaultSourceName() string { return s.src.Name() }

func (s *AudioStream) checkDeviceChange() bool {
	ws, ok := s.src.(*wasapiSource)
	if !ok {
		return false
	}
	cur, err := defaultEndpointID(s.Label)
	if err != nil || cur == "" {
		return false
	}
	return cur != ws.DefaultDeviceID()
}
