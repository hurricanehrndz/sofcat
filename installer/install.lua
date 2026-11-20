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
-- locked), then the steps of a fresh install follow, with the config path
-- the service was registered with. A failed upgrade rolls back the files and
-- the ARP entry, and the undo reinstalls the previous service. Two limits: a service that cannot be deleted because a handle to
-- it is open (the Services console) is only marked for deletion, so the
-- undo cannot reinstall it and the machine has no service until that handle
-- closes; the next run then finds none and installs over the previous
-- files. A service that does not stop within -serviceremove's timeout stays
-- registered but may end up stopped until a reboot. An install made with
-- /D= is not upgraded.
-- Nothing here touches %ProgramData%\@PRODUCT@.

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

-- One argument as Go's syscall.EscapeArg writes it: quoted when it has a
-- space or tab, bare otherwise. Returns it and the text after it, or nil.
-- A quoted one ending in a backslash, which EscapeArg would have doubled,
-- is refused rather than un-escaped.
local function arg(s)
  local quoted, rest = s:match('^"([^"]*)"(.*)$')
  if quoted then
    if quoted:find("[ \t]") and not quoted:find("\\$") then
      return quoted, rest
    end
    return nil
  end
  return s:match('^([^%s"]+)(.*)$')
end

-- sc.exe query exits 0 only for a service that exists. Every refusal comes
-- before anything is stopped or copied, so a rollback never touches it.
local upgrade, dangling, config = false, false, nil
local query = embala.exec{ cmd = "sc.exe", args = { "query", "@APP@" } }
if query.code == 0 then
  -- -serviceinstall registers <exe> -c <config> -service, each path quoted
  -- when it has a space, as under Program Files. Anything else is refused:
  -- the upgrade would register it again in that form.
  local image = reg("SYSTEM\\CurrentControlSet\\Services\\@APP@", "ImagePath") or ""
  local path, rest = arg(image)
  if not path or path:lower() ~= exe:lower() then
    error("the @APP@ service already exists and does not run " .. exe .. "; remove it first")
  end
  local value, tail = rest:match("^ %-c (.+) %-service$"), nil
  if value then
    config, tail = arg(value)
  end
  if not config or tail ~= "" then
    error("the @APP@ service runs " .. image .. ", not the form -serviceinstall writes (" .. exe .. " -c <config> -service); remove it first")
  end
  -- Management tooling only moves forward: a newer install stays. A missing
  -- or unreadable version proceeds, as does the same one (a repair).
  local installed = reg("Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\@IDENTIFIER@", "DisplayVersion")
  local have, want = triple(installed), triple("@VERSION@")
  if have and want and newer(have, want) then
    error("@PRODUCT@ " .. installed .. " is installed, newer than @VERSION@")
  end
  -- The script has no io library, so a run of -version tells whether the
  -- installed @APP@.exe is there: exec raises "(os error 2)" (file) or
  -- "(os error 3)" (path) for one that is not. Any other failure stops here.
  local ok, err = pcall(embala.exec, { cmd = exe, args = { "-version" } })
  if not ok then
    if not tostring(err):find("%(os error [23]%)") then
      error(err, 0)
    end
    dangling = true
  else
    -- -servicestatus reads the config as -serviceinstall does and exits 0
    -- whatever state the service is in, so a failure means a config that
    -- would fail the new -serviceinstall and the rollback's, leaving no
    -- service. It changes nothing. A dangling service is not checked.
    local status = embala.exec{ cmd = exe, args = { "-c", config, "-servicestatus" } }
    if status.code ~= 0 then
      error("@APP@.exe -c " .. config .. " -servicestatus exited " .. status.code .. ": " .. (status.stderr or "") .. (status.stdout or ""))
    end
  end
  upgrade = true
end

-- A per-machine install defaults to ${ProgramFiles}\@APP@; the product
-- name is the directory the service and the docs expect.
embala.ui.page{ kind = "directory", default = os.getenv("ProgramFiles") .. "\\@PRODUCT@", allow_change = false }

if upgrade then
  -- exec does not wait for the wizard (no /S), so a gated call opens it first.
  -- mkdir records nothing for a directory that exists, and with /S it is
  -- only that. Afterwards embala.install_dir is final.
  embala.fs.mkdir(".")
  local dir = exe:match("^(.*)\\")
  if (embala.install_dir:gsub("\\+$", "")):lower() ~= dir:lower() then
    error("@PRODUCT@ is installed in " .. dir .. ", not " .. embala.install_dir .. "; an install made with /D= is not upgraded in place")
  end
  if dangling then
    -- The service runs an @APP@.exe that is gone, so nothing could ever
    -- remove it. Delete it, with no undo, and install afresh.
    embala.exec{ cmd = "sc.exe", args = { "stop", "@APP@" } }
    local delete = embala.exec{ cmd = "sc.exe", args = { "delete", "@APP@" } }
    if delete.code ~= 0 then
      error("sc.exe delete @APP@ exited " .. delete.code .. ": " .. (delete.stdout or ""))
    end
  else
    -- Stops and deletes the service, removes the Run value and ends the
    -- helper and storefront. On rollback, once the old @APP@.exe is restored,
    -- the undo installs and starts its service again, with its config path.
    local remove = embala.exec{ cmd = exe, args = { "-serviceremove" },
                                undo = { cmd = exe, args = { "-c", config, "-serviceinstall" } } }
    if remove.code ~= 0 then
      error("the installed @APP@.exe -serviceremove exited " .. remove.code .. ": " .. (remove.stderr or "") .. (remove.stdout or ""))
    end
  end
end

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

-- Registers and starts the service and writes the helper's Run value; an
-- upgrade keeps the config path the service had. A failure fails the
-- install, which rolls the copies above back. The new -serviceremove first
-- lets go of anything it left running from the new executables, so
-- rollback can restore the old ones.
local args = { "-serviceinstall" }
if config then
  args = { "-c", config, "-serviceinstall" }
end
local install = embala.exec{ cmd = embala.install_dir .. "\\@APP@.exe", args = args }
if install.code ~= 0 then
  embala.exec{ cmd = embala.install_dir .. "\\@APP@.exe", args = { "-serviceremove" } }
  error("@APP@.exe -serviceinstall exited " .. install.code .. ": " .. (install.stderr or "") .. (install.stdout or ""))
end
