package core

func init() {
	if buildAllSources == "true" {
		RegisterFlavor("zhenguojian", nil)
		return
	}
	RegisterFlavor("hongguojian", []string{"hongguo"})
}
