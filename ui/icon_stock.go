//go:build !brand

package main

import _ "embed"

// defaultIcon is the window and taskbar icon when no branding logo is set.
// The executable's own icon resource (linked by `just winres`) is what
// Explorer and the Start menu show; the window icon is set separately. A
// rebranded build (`just brand=… build`) embeds brand-icon.png instead.
//
//go:embed icon.png
var defaultIcon []byte
