-- @PRODUCT@ install script for embala's setup.exe (see embala.toml beside
-- this file). `just setup` renders the @PLACEHOLDERS@ into build/setup/.
--
-- Every copy, the shortcut and the ARP entry are logged to install.log, so
-- the uninstaller reverses them, and journaled, so an error here rolls them
-- back: a file or ARP value this run overwrote gets its old contents back.
-- The service and the per-user helper's logon entry belong to @APP@.exe
-- -serviceinstall, which uninstall.lua undoes with -serviceremove.
-- embala.package is never read: the uninstaller may lack it.
--
-- Over an existing install of this product the run is an upgrade: the
-- installed @APP@.exe -serviceremove stops the service (so no executable is
-- locked), then the steps of a fresh install follow. A failed upgrade rolls
-- back to the previous version with its service running. Nothing here
-- touches %ProgramData%\@PRODUCT@.

local exe = os.getenv("ProgramFiles") .. "\\@PRODUCT@\\@APP@.exe"

-- A registry string, or nil when it is missing or cannot be read.
local function reg(key, name)
  local ok, value = pcall(embala.registry.get, { hive = "HKLM", key = key, name = name })
  if ok and type(value) == "string" then
    return value
  end
end

-- The numeric major.minor.patch of a version, or nil. Pre-release and build
-- suffixes are ignored, so 5.8.0-beta.1 and 5.8.0 compare equal.
local function triple(v)
  local a, b, c = (v or ""):match("^(%d+)%.(%d+)%.(%d+)")
  if a then
    return { tonumber(a), tonumber(b), tonumber(c) }
  end
end

local function newer(a, b)
  for i = 1, 3 do
    if a[i] ~= b[i] then
      return a[i] > b[i]
    end
  end
  return false
end

-- sc.exe query exits 0 only for a service that exists. Every refusal comes
-- before anything is stopped or copied, so a rollback never touches it.
local query = embala.exec{ cmd = "sc.exe", args = { "query", "@APP@" } }
if query.code == 0 then
  -- -serviceinstall registers "<exe>" -c <config> -service; the path is
  -- quoted when it has a space, as under Program Files.
  local image = reg("SYSTEM\\CurrentControlSet\\Services\\@APP@", "ImagePath") or ""
  local path = image:match('^"([^"]*)"') or image:match("^(%S+)") or ""
  if path:lower() ~= exe:lower() then
    error("the @APP@ service already exists and does not run " .. exe .. "; remove it first")
  end
  -- Management tooling only moves forward: a newer install stays. A missing
  -- or unreadable version proceeds, as does the same one (a repair).
  local installed = reg("Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\@IDENTIFIER@", "DisplayVersion")
  local have, want = triple(installed), triple("@VERSION@")
  if have and want and newer(have, want) then
    error("@PRODUCT@ " .. installed .. " is installed, newer than @VERSION@")
  end
  -- Stops and deletes the service, removes the Run value and ends the helper
  -- and storefront. On rollback, once the old @APP@.exe is restored, the undo
  -- installs and starts its service again. In the wizard (no /S) this runs
  -- before the wizard opens, since exec is not gated on it.
  local remove = embala.exec{ cmd = exe, args = { "-serviceremove" },
                              undo = { cmd = exe, args = { "-serviceinstall" } } }
  if remove.code ~= 0 then
    error("the installed @APP@.exe -serviceremove exited " .. remove.code .. ": " .. (remove.stderr or "") .. (remove.stdout or ""))
  end
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
-- failure fails the install, which rolls the copies above back. The new
-- -serviceremove first lets go of anything it left running from the new
-- executables, so rollback can restore the old ones.
local install = embala.exec{ cmd = embala.install_dir .. "\\@APP@.exe", args = { "-serviceinstall" } }
if install.code ~= 0 then
  embala.exec{ cmd = embala.install_dir .. "\\@APP@.exe", args = { "-serviceremove" } }
  error("@APP@.exe -serviceinstall exited " .. install.code .. ": " .. (install.stderr or "") .. (install.stdout or ""))
end
