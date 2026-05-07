package encoder

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// parseProgressStream reads FFmpeg `-progress pipe:2` key=value lines
// from r and writes the latest values into p. Returns when r returns
// EOF or any error other than io.EOF.
func parseProgressStream(r io.Reader, p *Progress, capture io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if capture != nil {
			_, _ = capture.Write([]byte(line + "\n"))
		}
		key, val, ok := splitKV(line)
		if !ok {
			continue
		}
		switch key {
		case "frame":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				if cur := p.FrameCount.Load(); n > cur {
					p.FrameCount.Store(n)
				}
			}
		case "out_time_us":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				if cur := p.OutTimeUs.Load(); n > cur {
					p.OutTimeUs.Store(n)
				}
			}
		}
	}
	return sc.Err()
}

func splitKV(line string) (string, string, bool) {
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}
