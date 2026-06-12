//go:build lua_wangshu

package lua

// init makes binaries built with `-tags=lua_wangshu` fail fast with a clear
// message, instead of silently falling back to gopher-lua. The wangshu backend
// implementation is on its way:
//
//   - wangshu issue #1 closed; v0.1.1 published with the SetGlobal / GetGlobal /
//     State.Call surface the operator needs:
//     https://github.com/Liam0205/wangshu/releases/tag/v0.1.1
//   - the actual gopherEngine-shaped wangshu adapter lands in a follow-up
//     commit on this branch (see backend.go for the Pool / Engine contract).
//
// Until that commit, do not enable -tags=lua_wangshu in production builds.
func init() {
	panic("lua_wangshu build tag is set, but the wangshu backend adapter has " +
		"not landed yet on this branch. Drop -tags=lua_wangshu to use the " +
		"default gopher-lua backend.")
}
