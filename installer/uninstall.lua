-- @PRODUCT@ uninstall script for the uninstall.exe that setup.exe leaves in
-- the install directory. It runs before the uninstaller replays install.log
-- (the executables, the shortcut and the ARP entry) and removes itself and
-- the directory. @APP@.exe -serviceremove stops the service, waits for it
-- (so the executable can be deleted), deletes it, removes the helper's
-- Run value and ends any helper still running from this directory. A
-- failure here is reported and the replay still runs.
-- embala.install_dir is the uninstaller's own directory.
local remove = embala.exec{ cmd = embala.install_dir .. "\\@APP@.exe", args = { "-serviceremove" } }
if remove.code ~= 0 then
  error("@APP@.exe -serviceremove exited " .. remove.code .. ": " .. (remove.stderr or "") .. (remove.stdout or ""))
end
