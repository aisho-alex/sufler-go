package capture

import "encoding/binary"

// decimate48to16 конвертирует моно int16 LE 48 кГц в float32 16 кГц,
// усредняя каждую тройку сэмплов (48000/16000 = 3).
func decimate48to16(in []byte) []float32 {
	n := len(in) / 6
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		b := in[i*6:]
		s0 := int16(binary.LittleEndian.Uint16(b))
		s1 := int16(binary.LittleEndian.Uint16(b[2:]))
		s2 := int16(binary.LittleEndian.Uint16(b[4:]))
		out[i] = (float32(s0) + float32(s1) + float32(s2)) / (3 * 32768)
	}
	return out
}
