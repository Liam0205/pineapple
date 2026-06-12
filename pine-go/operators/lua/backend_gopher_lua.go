//go:build !lua_wangshu

// 默认 Lua backend:gopher-lua(github.com/yuin/gopher-lua v1.1.2)。
// 实际实现位于 lua.go / pool.go;本文件作为 build-tag selector 占位。
//
// 切换到 wangshu(https://github.com/Liam0205/wangshu)走:
//
//	go build -tags=lua_wangshu ./...
//
// 当前阻塞在 wangshu 公共 API 缺 SetGlobal / GetGlobal:
//
//	https://github.com/Liam0205/wangshu/issues/1
//
// upstream 落地后,backend_wangshu.go 的 stub 会被替换为 wangshu 实装,
// 且 lua.go / pool.go 会拆出 backend interface 让两实现共存。
package lua
