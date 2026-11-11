//go:build !zhenguojian

package sources

import "duanjuapp/native/core"

func init() {
	core.RegisterFlavor("hongguojian", []string{"hongguo"})
}
