package chaoxing

import "strconv"

// SecretFontDecodeAudit 只暴露维护计数，不返回页面文字、字体或身份信息。
type SecretFontDecodeAudit struct {
	Resolved        int `json:"resolved"`
	Reference       int `json:"reference"`
	Legacy          int `json:"legacy"`
	Empty           int `json:"empty"`
	CorrectedLegacy int `json:"corrected_legacy"`
}

// AuditSecretFontDecoding 对已经保存的页面执行真实解码路径，不发送网络请求。
func AuditSecretFontDecoding(payload []byte, useLegacy bool) (SecretFontDecodeAudit, error) {
	var report SecretFontDecodeAudit
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		return report, err
	}
	if !useLegacy {
		decoder.index = map[string]rune{}
	}
	failure := rewriteSecretText(parseHTMLDocument(payload), decoder)
	report.Resolved = len(decoder.cache)
	report.Empty = len(decoder.empty)
	for code, value := range decoder.cache {
		signature := decoder.signatures[code]
		matched := secretReferenceMatch{}
		if decoder.reference != nil {
			key := strconv.Itoa(decoder.font.unitsPerEm) + ":" + signature
			matched = decoder.reference.matchCached(decoder.outline, decoder.font.cmap[code], key)
		}
		if matched.found {
			report.Reference++
		} else {
			report.Legacy++
		}
		if previous, ok := secretFontIndex()[signature]; ok && previous != value {
			report.CorrectedLegacy++
		}
	}
	return report, failure
}
