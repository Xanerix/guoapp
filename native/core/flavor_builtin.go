package core

func init() {
	if buildAllSources == "true" {
		RegisterFlavor("zhenguojian", []string{
			"hongguo", "hanxiaoquan", "guipian", "sorani",
			"huangdou", "huangju", "yeguo", "dsd", "jinguoduanju", "jinguo", "heitao", "beabox",
			"huangguo-video", "huangguoai", "cloudfront",
		})
		return
	}
	RegisterFlavor("hongguojian", []string{"hongguo"})
}
