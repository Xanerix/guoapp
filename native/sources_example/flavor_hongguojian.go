//go:build !zhenguojian

package example

import "duanjuapp/native/core"

func init() {
	core.RegisterFlavor("hongguojian", []string{sourceExample})
}
