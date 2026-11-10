//go:build zhenguojian

package core

func init() {
	RegisterFlavor("zhenguojian", []string{
		"hongguo", "hanxiaoquan", "guipian", "sorani",
		"huangdou", "huangju", "yeguo", "dsd", "jinguoduanju", "jinguo", "heitao", "beabox",
		"huangguo-video", "huangguoai", "cloudfront",
	})
}
