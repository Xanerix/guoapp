//go:build zhenguojian

package sources

import "duanjuapp/native/core"

func init() {
	core.RegisterFlavor("zhenguojian", []string{
		"hongguo", "hanxiaoquan", "guipian", "sorani",
		"huangdou", "huangju", "yeguo", "dsd", "jinguoduanju", "jinguo", "heitao", "beabox",
		"huangguo-video", "huangguoai", "cloudfront",
	})
}
