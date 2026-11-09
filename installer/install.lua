-- @PRODUCT@ install script for embala's setup.exe (see embala.toml beside
-- this file). `just setup` renders the @PLACEHOLDERS@ into build/setup/.
--
-- Every copy, the shortcut and the ARP entry are logged to install.log, so
-- the uninstaller reverses them and an error here rolls them back. The
-- service and the per-user helper's logon entry belong to @APP@.exe
-- -serviceinstall, which uninstall.lua undoes with -serviceremove.
-- embala.package is never read: the uninstaller may lack it.

-- Refuse an install over an existing one before anything is copied, so a
-- rollback never touches it. sc.exe query exits 0 only for a service that
-- exists.
local query = embala.exec{ cmd = "sc.exe", args = { "query", "@APP@" } }
if query.code == 0 then
  error("the @APP@ service already exists; uninstall @PRODUCT@ first")
end

-- A per-machine install defaults to ${ProgramFiles}\@APP@; the product
-- name is the directory the service and the docs expect.
embala.ui.page{ kind = "directory", default = os.getenv("ProgramFiles") .. "\\@PRODUCT@", allow_change = false }

embala.fs.copy("@APP@.exe", "@APP@.exe")
embala.fs.copy("@APP@-ui.exe", "@APP@-ui.exe")
embala.fs.copy("@APP@-useragent.exe", "@APP@-useragent.exe")

embala.shortcut.create{ name = "@PRODUCT@", target = "@APP@-ui.exe", location = "start-menu", description = "@DESCRIPTION@" }

embala.arp.register{
  display_name = "@PRODUCT@",
  version = "@VERSION@",
  publisher = "@PUBLISHER@",
  install_location = embala.install_dir,
  uninstall_string = '"' .. embala.install_dir .. '\\uninstall.exe"',
  display_icon = embala.install_dir .. '\\@APP@-ui.exe',
}

-- Registers and starts the service and writes the helper's Run value. A
-- failure fails the install, which rolls the copies above back.
local install = embala.exec{ cmd = embala.install_dir .. "\\@APP@.exe", args = { "-serviceinstall" } }
if install.code ~= 0 then
  error("@APP@.exe -serviceinstall exited " .. install.code .. ": " .. (install.stderr or "") .. (install.stdout or ""))
end
