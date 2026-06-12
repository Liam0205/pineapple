//go:build lua_wangshu

package lua

// init makes binaries built with `-tags=lua_wangshu` fail fast with a clear
// message, instead of silently falling back to gopher-lua. The wangshu backend
// is not implemented yet — see:
//
//	https://github.com/Liam0205/wangshu/issues/1
//
// 一旦 upstream issue 接受并发布,本文件会被替换为:
//   - 实装 backend interface(借用 wangshu.Compile / NewState / Program.Run +
//     新增的 SetGlobal / GetGlobal 桥接)
//   - 注册为 default backend(替换 gopher-lua 注册路径)
func init() {
	panic("lua_wangshu build tag is set, but the wangshu backend is not yet " +
		"implemented (blocked on https://github.com/Liam0205/wangshu/issues/1). " +
		"Build without -tags=lua_wangshu to use the default gopher-lua backend.")
}
