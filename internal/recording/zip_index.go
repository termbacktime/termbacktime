package recording

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Bound the central directory before archive/zip allocates its entry table.
// v1 archives fit ordinary ZIP limits and never require multi-disk or ZIP64 indexes.
func checkZIPIndex(reader io.ReaderAt, size int64) error {
	tail := make([]byte, min(size, int64(65557)))
	if _, err := reader.ReadAt(tail, size-int64(len(tail))); err != nil {
		return err
	}
	for i := len(tail) - 22; i >= 0; i-- {
		b := tail[i:]
		if binary.LittleEndian.Uint32(b) != 0x06054b50 || i+22+int(binary.LittleEndian.Uint16(b[20:])) != len(tail) {
			continue
		}
		count := binary.LittleEndian.Uint16(b[10:])
		centralSize := binary.LittleEndian.Uint32(b[12:])
		offset := binary.LittleEndian.Uint32(b[16:])
		if binary.LittleEndian.Uint32(b[4:]) != 0 || binary.LittleEndian.Uint16(b[8:]) != count || count < 2 || count > 4098 || centralSize > 16<<20 || int64(offset)+int64(centralSize) > size-int64(len(b)) {
			return fmt.Errorf("invalid ZIP index")
		}
		return nil
	}
	return fmt.Errorf("missing ZIP index")
}
