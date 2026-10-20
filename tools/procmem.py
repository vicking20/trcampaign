"""Read/write another process's memory on Linux (/proc/<pid>/mem) and Windows (ReadProcessMemory via ctypes).
The Windows branch is untested. API: find_pid(exe_name) -> int|None ; Mem(pid, write=False).read(addr, n) / .write(addr, data)."""
import os, struct, sys

IS_WINDOWS = sys.platform.startswith("win")

def find_pid(exe_name):
    """pid of the process running <exe_name> (Linux: Wine process whose command line has it, not the wrappers)."""
    if not IS_WINDOWS:
        for p in os.listdir("/proc"):                       # the real process has comm == exe name (truncated to 15 chars); wine's launcher does not
            if p.isdigit():
                try:
                    if open(f"/proc/{p}/comm").read().strip().lower() == exe_name[:15].lower(): return int(p)
                except OSError: pass
        for p in os.listdir("/proc"):
            if p.isdigit():
                try: c = open(f"/proc/{p}/cmdline", "rb").read().decode(errors="ignore")
                except OSError: continue
                if exe_name in c and not any(x in c for x in ("umu", "bash", "python", "start_server", "systemd")):
                    return int(p)
        return None
    import ctypes
    from ctypes import wintypes
    TH32CS_SNAPPROCESS = 2
    class PROCESSENTRY32(ctypes.Structure):
        _fields_ = [("dwSize", wintypes.DWORD), ("cntUsage", wintypes.DWORD), ("th32ProcessID", wintypes.DWORD),
                    ("th32DefaultHeapID", ctypes.c_size_t), ("th32ModuleID", wintypes.DWORD), ("cntThreads", wintypes.DWORD),
                    ("th32ParentProcessID", wintypes.DWORD), ("pcPriClassBase", ctypes.c_long), ("dwFlags", wintypes.DWORD),
                    ("szExeFile", ctypes.c_char * 260)]
    k = ctypes.windll.kernel32
    snap = k.CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0)
    e = PROCESSENTRY32(); e.dwSize = ctypes.sizeof(e)
    found = None
    ok = k.Process32First(snap, ctypes.byref(e))
    while ok:
        if e.szExeFile.decode(errors="ignore").lower() == exe_name.lower(): found = e.th32ProcessID; break
        ok = k.Process32Next(snap, ctypes.byref(e))
    k.CloseHandle(snap)
    return found

def alive(pid):
    if not IS_WINDOWS: return os.path.exists(f"/proc/{pid}")
    import ctypes
    k = ctypes.windll.kernel32; k.OpenProcess.restype = ctypes.c_void_p
    h = k.OpenProcess(0x1000, False, pid)                                      # PROCESS_QUERY_LIMITED_INFORMATION
    if not h: return False
    code = ctypes.c_ulong(0); k.GetExitCodeProcess(ctypes.c_void_p(h), ctypes.byref(code)); k.CloseHandle(ctypes.c_void_p(h))
    return code.value == 259

class Mem:
    def __init__(self, pid, write=False):
        self.pid, self.write_ok = pid, write
        if IS_WINDOWS:
            import ctypes
            access = 0x0010 | 0x0400 | (0x0020 | 0x0008 if write else 0)       # VM_READ, QUERY_INFORMATION, VM_WRITE, VM_OPERATION
            self._k = ctypes.windll.kernel32
            self._k.OpenProcess.restype = ctypes.c_void_p
            self._h = self._k.OpenProcess(access, False, pid)
            if not self._h: raise OSError("OpenProcess failed (run as the same user / administrator)")
        else:
            self._f = open(f"/proc/{pid}/mem", "r+b" if write else "rb", 0)

    def read(self, addr, n):
        if not IS_WINDOWS:
            self._f.seek(addr); return self._f.read(n)
        import ctypes
        out = bytearray()
        for off in range(0, n, 4096):                                           # page by page: unreadable pages become zeros
            m = min(4096, n - off); buf = ctypes.create_string_buffer(m); got = ctypes.c_size_t(0)
            ok = self._k.ReadProcessMemory(ctypes.c_void_p(self._h), ctypes.c_void_p(addr + off), buf, m, ctypes.byref(got))
            out += buf.raw[:m] if ok else b"\0" * m
        return bytes(out)

    def write(self, addr, data):
        if not IS_WINDOWS:
            self._f.seek(addr); self._f.write(data); return
        import ctypes
        put = ctypes.c_size_t(0)
        if not self._k.WriteProcessMemory(ctypes.c_void_p(self._h), ctypes.c_void_p(addr), data, len(data), ctypes.byref(put)):
            raise OSError("WriteProcessMemory failed")

    def u32(self, addr): return struct.unpack("<I", self.read(addr, 4))[0]

    def close(self):
        if IS_WINDOWS:
            import ctypes; self._k.CloseHandle(ctypes.c_void_p(self._h))
        else: self._f.close()
