package adminpage

import "strings"

func HTML() []byte {
	var b strings.Builder
	b.WriteString(pageStart)
	b.WriteString(pageStyles)
	b.WriteString(pageMiddle)
	b.WriteString(pageBody)
	b.WriteString(pageScriptOpen)
	b.WriteString(pageScript)
	b.WriteString(pageEnd)
	return []byte(b.String())
}
