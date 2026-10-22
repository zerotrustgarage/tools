// RC0 only. Creates disposable local users/shares on a disposable Windows runner.
// Password characters exist only in allocated native buffers; none enter a
// managed string, command line, environment variable, output object or log.
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;

namespace Ztg.CopySpike
{
    public static class NetworkSpike
    {
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        struct UserInfo
        {
            [MarshalAs(UnmanagedType.LPWStr)] public string Name;
            public IntPtr Password;
            public uint PasswordAge, Privilege;
            [MarshalAs(UnmanagedType.LPWStr)] public string Home;
            [MarshalAs(UnmanagedType.LPWStr)] public string Comment;
            public uint Flags;
            [MarshalAs(UnmanagedType.LPWStr)] public string Script;
        }

        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        struct ShareInfo
        {
            [MarshalAs(UnmanagedType.LPWStr)] public string Name;
            public uint Type;
            [MarshalAs(UnmanagedType.LPWStr)] public string Remark;
            public uint Permissions, MaxUses, CurrentUses;
            [MarshalAs(UnmanagedType.LPWStr)] public string Path;
            public IntPtr Password;
            public uint Reserved;
            public IntPtr SecurityDescriptor;
        }

        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        struct Resource
        {
            public uint Scope, Type, DisplayType, Usage;
            [MarshalAs(UnmanagedType.LPWStr)] public string Local;
            [MarshalAs(UnmanagedType.LPWStr)] public string Remote;
            [MarshalAs(UnmanagedType.LPWStr)] public string Comment;
            [MarshalAs(UnmanagedType.LPWStr)] public string Provider;
        }

        [StructLayout(LayoutKind.Sequential)]
        struct GroupMember { public IntPtr Sid; }

        [StructLayout(LayoutKind.Sequential)]
        struct Startup
        {
            public uint Size;
            public IntPtr Reserved, Desktop, Title;
            public uint X, Y, XSize, YSize, XCountChars, YCountChars, FillAttribute, Flags;
            public ushort ShowWindow, ReservedLength;
            public IntPtr ReservedBytes, Input, Output, Error;
        }

        [StructLayout(LayoutKind.Sequential)]
        struct ProcessInfo
        {
            public IntPtr Process, Thread;
            public uint ProcessId, ThreadId;
        }

        [StructLayout(LayoutKind.Sequential)]
        struct IoCounters
        {
            public ulong ReadOperations, WriteOperations, OtherOperations;
            public ulong ReadBytes, WriteBytes, OtherBytes;
        }

        [StructLayout(LayoutKind.Sequential)]
        struct BasicLimits
        {
            public long ProcessTime, JobTime;
            public uint Flags;
            public UIntPtr MinimumWorkingSet, MaximumWorkingSet;
            public uint ActiveProcesses;
            public UIntPtr Affinity;
            public uint Priority, Scheduling;
        }

        [StructLayout(LayoutKind.Sequential)]
        struct ExtendedLimits
        {
            public BasicLimits Basic;
            public IoCounters Io;
            public UIntPtr ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory;
        }

        [DllImport("netapi32.dll", CharSet = CharSet.Unicode)]
        static extern uint NetUserAdd(string server, uint level, ref UserInfo info, out uint parameter);
        [DllImport("netapi32.dll", CharSet = CharSet.Unicode)]
        static extern uint NetUserDel(string server, string user);
        [DllImport("netapi32.dll", CharSet = CharSet.Unicode)]
        static extern uint NetLocalGroupAddMembers(string server, string group, uint level, ref GroupMember member, uint count);
        [DllImport("netapi32.dll", CharSet = CharSet.Unicode)]
        static extern uint NetShareAdd(string server, uint level, ref ShareInfo info, out uint parameter);
        [DllImport("netapi32.dll", CharSet = CharSet.Unicode)]
        static extern uint NetShareDel(string server, string share, uint reserved);
        [DllImport("mpr.dll", CharSet = CharSet.Unicode)]
        static extern uint WNetAddConnection2(ref Resource resource, IntPtr password, string user, uint flags);
        [DllImport("mpr.dll", CharSet = CharSet.Unicode)]
        static extern uint WNetCancelConnection2(string resource, uint flags, bool force);
        [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(string sddl, uint revision, out IntPtr descriptor, out uint size);
        [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern bool SetFileSecurity(string path, uint information, IntPtr descriptor);
        [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern bool CreateProcessWithLogonW(string user, string domain, IntPtr password, uint logonFlags,
            string application, StringBuilder command, uint creationFlags, IntPtr environment, string directory,
            ref Startup startup, out ProcessInfo process);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern IntPtr CreateJobObject(IntPtr attributes, string name);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool SetInformationJobObject(IntPtr job, uint infoClass, ref ExtendedLimits info, uint length);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool IsProcessInJob(IntPtr process, IntPtr job, out bool inJob);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern uint ResumeThread(IntPtr thread);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool GetExitCodeProcess(IntPtr process, out uint code);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool GetProcessIoCounters(IntPtr process, out IoCounters counters);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool TerminateProcess(IntPtr process, uint code);
        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool CloseHandle(IntPtr handle);
        [DllImport("kernel32.dll")]
        static extern IntPtr LocalFree(IntPtr pointer);

        sealed class NativePassword : IDisposable
        {
            IntPtr memory;
            const int Length = 32;
            public bool Zeroed { get; private set; }
            public IntPtr Pointer
            {
                get
                {
                    if (memory == IntPtr.Zero || Zeroed) throw new InvalidOperationException("Password buffer is unavailable.");
                    return memory;
                }
            }

            public NativePassword()
            {
                memory = Marshal.AllocHGlobal((Length + 1) * 2);
                for (int i = 0; i <= Length; i++) Marshal.WriteInt16(memory, i * 2, 0);
                byte[] entropy = new byte[Length];
                try
                {
                    RandomNumberGenerator.Fill(entropy);
                    const string alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#%+=-_";
                    for (int i = 0; i < Length; i++)
                        Marshal.WriteInt16(memory, i * 2, (short)alphabet[entropy[i] % alphabet.Length]);
                    // Guarantee complexity without constructing a password string.
                    Marshal.WriteInt16(memory, 0, (short)'A');
                    Marshal.WriteInt16(memory, 2, (short)'a');
                    Marshal.WriteInt16(memory, 4, (short)'9');
                    Marshal.WriteInt16(memory, 6, (short)'!');
                }
                catch
                {
                    Dispose();
                    throw;
                }
                finally { Array.Clear(entropy, 0, entropy.Length); }
            }

            public void Clear()
            {
                if (memory == IntPtr.Zero) return;
                for (int i = 0; i <= Length; i++) Marshal.WriteInt16(memory, i * 2, 0);
                Zeroed = true;
                for (int i = 0; i <= Length; i++)
                    if (Marshal.ReadInt16(memory, i * 2) != 0) Zeroed = false;
                if (!Zeroed) throw new InvalidOperationException("Native buffer clearing failed.");
            }

            public void Dispose()
            {
                if (memory == IntPtr.Zero) return;
                try { Clear(); }
                finally { Marshal.FreeHGlobal(memory); memory = IntPtr.Zero; }
            }
        }

        static void Require(bool value, string stage)
        {
            if (!value) throw new InvalidOperationException(stage);
        }

        static void Status(uint status, string stage)
        {
            if (status != 0) throw new InvalidOperationException(stage + " returned " + status);
        }

        static void Win32(bool value, string stage)
        {
            if (!value) throw new Win32Exception(Marshal.GetLastWin32Error(), stage);
        }

        static SecurityIdentifier AddUser(string name, NativePassword password, List<Action> cleanup)
        {
            UserInfo info = new UserInfo { Name = name, Password = password.Pointer, Privilege = 1, Flags = 0x201,
                Comment = "Disposable ZTG Copy RC0 fixture" };
            uint parameter;
            Status(NetUserAdd(null, 1, ref info, out parameter), "NetUserAdd");
            cleanup.Add(() => Status(NetUserDel(null, name), "NetUserDel"));
            SecurityIdentifier sid = (SecurityIdentifier)new NTAccount(Environment.MachineName, name).Translate(typeof(SecurityIdentifier));
            SecurityIdentifier users = new SecurityIdentifier(WellKnownSidType.BuiltinUsersSid, null);
            string group = users.Translate(typeof(NTAccount)).Value;
            group = group.Substring(group.LastIndexOf('\\') + 1);
            byte[] bytes = new byte[sid.BinaryLength];
            sid.GetBinaryForm(bytes, 0);
            IntPtr nativeSid = Marshal.AllocHGlobal(bytes.Length);
            try
            {
                Marshal.Copy(bytes, 0, nativeSid, bytes.Length);
                GroupMember member = new GroupMember { Sid = nativeSid };
                uint status = NetLocalGroupAddMembers(null, group, 0, ref member, 1);
                if (status != 1378) Status(status, "NetLocalGroupAddMembers");
            }
            finally { Marshal.FreeHGlobal(nativeSid); }
            return sid;
        }

        static void AddShare(string name, string path, IntPtr descriptor, List<Action> cleanup)
        {
            ShareInfo info = new ShareInfo { Name = name, Path = path, MaxUses = UInt32.MaxValue,
                Remark = "Disposable ZTG Copy RC0 fixture", SecurityDescriptor = descriptor };
            uint parameter;
            Status(NetShareAdd(null, 502, ref info, out parameter), "NetShareAdd");
            cleanup.Add(() => Status(NetShareDel(null, name, 0), "NetShareDel"));
        }

        static uint Connect(string remote, string user, NativePassword password)
        {
            Resource resource = new Resource { Type = 1, Remote = remote };
            return WNetAddConnection2(ref resource, password.Pointer, Environment.MachineName + "\\" + user, 4);
        }

        static bool Equal(byte[] a, byte[] b)
        {
            if (a.Length != b.Length) return false;
            for (int i = 0; i < a.Length; i++) if (a[i] != b[i]) return false;
            return true;
        }

        // Only generated paths without quotes or trailing separators are used.
        // No shell is started, and this command never contains a password.
        static string Quote(string path)
        {
            Require(!path.Contains("\"") && !path.EndsWith("\\"), "Unexpected fixture path for quoting.");
            return "\"" + path + "\"";
        }

        static Dictionary<string, object> NetOnly(string user, NativePassword password, string engine,
            string source, string destination, string output, bool stop)
        {
            Dictionary<string, object> result = new Dictionary<string, object>();
            IntPtr job = CreateJobObject(IntPtr.Zero, null);
            Win32(job != IntPtr.Zero, "CreateJobObject");
            ProcessInfo process = new ProcessInfo();
            try
            {
                ExtendedLimits limits = new ExtendedLimits();
                limits.Basic.Flags = 0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
                Win32(SetInformationJobObject(job, 9, ref limits, (uint)Marshal.SizeOf<ExtendedLimits>()), "SetInformationJobObject");
                Startup startup = new Startup { Size = (uint)Marshal.SizeOf<Startup>() };
                string command = Quote(engine) + " " + Quote(source) + " " + Quote(destination) +
                    " /E /XX /R:0 /W:0 /XJ /COPY:DAT " + Quote("/UNILOG:" + output);
                if (stop) command += " /MON:1 /MOT:1";
                Win32(CreateProcessWithLogonW(user, Environment.MachineName, password.Pointer, 2,
                    engine, new StringBuilder(command), 0x08000004, IntPtr.Zero, source, ref startup, out process), "CreateProcessWithLogonW");
                // Assign while suspended: no copy can run outside containment.
                Win32(AssignProcessToJobObject(job, process.Process), "AssignProcessToJobObject");
                bool inJob;
                Win32(IsProcessInJob(process.Process, job, out inJob), "IsProcessInJob");
                Require(inJob, "Net-only child is outside its job.");
                result["in_job_before_resume"] = inJob;
                Win32(ResumeThread(process.Thread) != UInt32.MaxValue, "ResumeThread");
                Stopwatch timeout = Stopwatch.StartNew();
                while (true)
                {
                    uint wait = WaitForSingleObject(process.Process, 100);
                    if (wait == 0)
                    {
                        Require(!stop, "Monitor child exited before the job-close test.");
                        break;
                    }
                    Win32(wait != UInt32.MaxValue, "WaitForSingleObject");
                    Require(wait == 258, "Unexpected wait result.");
                    if (stop && timeout.ElapsedMilliseconds >= 300)
                    {
                        Win32(CloseHandle(job), "Close job to stop");
                        job = IntPtr.Zero;
                        Require(WaitForSingleObject(process.Process, 5000) == 0, "Job close did not stop the child.");
                        result["stopped_by_job_close"] = true;
                        break;
                    }
                    Require(timeout.ElapsedMilliseconds < 30000, "Net-only fixture timed out.");
                }
                uint exit;
                Win32(GetExitCodeProcess(process.Process, out exit), "GetExitCodeProcess");
                result["native_exit"] = exit;
                IoCounters counters;
                bool queryable = GetProcessIoCounters(process.Process, out counters);
                result["io_queryable"] = queryable;
                result["read_bytes"] = counters.ReadBytes;
                result["write_bytes"] = counters.WriteBytes;
                Require(queryable, "Net-only process I/O was unavailable.");
                if (!stop) Require(exit < 8, "Net-only copy failed with native exit " + exit);
                return result;
            }
            finally
            {
                if (job != IntPtr.Zero) CloseHandle(job);
                if (process.Process != IntPtr.Zero)
                {
                    // Also handles failures before job assignment, while suspended.
                    TerminateProcess(process.Process, 3);
                    WaitForSingleObject(process.Process, 5000);
                    CloseHandle(process.Process);
                }
                if (process.Thread != IntPtr.Zero) CloseHandle(process.Thread);
            }
        }

        public static Dictionary<string, object> Run(string root, string engine, string output, Action<string> inspect)
        {
            Dictionary<string, object> result = new Dictionary<string, object>();
            result["schema"] = 1;
            result["passed"] = false;
            List<Action> cleanup = new List<Action>();
            List<string> cleanupErrors = new List<string>();
            NativePassword passwordA = null, passwordB = null;
            IntPtr descriptor = IntPtr.Zero;
            bool connectedA = false, connectedB = false;
            try
            {
                string suffix = Guid.NewGuid().ToString("N").Substring(0, 8);
                string userA = "ztg0a" + suffix, userB = "ztg0b" + suffix;
                string shareA = "ztg0a_" + suffix, shareB = "ztg0b_" + suffix;
                string remoteA = "\\\\localhost\\" + shareA, remoteB = "\\\\localhost\\" + shareB;
                passwordA = new NativePassword();
                passwordB = new NativePassword();
                SecurityIdentifier sidA = AddUser(userA, passwordA, cleanup);
                SecurityIdentifier sidB = AddUser(userB, passwordB, cleanup);
                string sddl = "D:P(A;OICI;FA;;;" + sidA.Value + ")(A;OICI;FA;;;" + sidB.Value +
                    ")(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)";
                uint size;
                Win32(ConvertStringSecurityDescriptorToSecurityDescriptor(sddl, 1, out descriptor, out size), "Convert security descriptor");
                string shareRoot = Path.Combine(root, "private share");
                Directory.CreateDirectory(shareRoot);
                Win32(SetFileSecurity(shareRoot, 0x80000004, descriptor), "Set fixture DACL");
                byte[] payload = Encoding.UTF8.GetBytes("Synthetic ZTG Copy SMB fixture.");
                File.WriteAllBytes(Path.Combine(shareRoot, "seed.txt"), payload);
                AddShare(shareA, shareRoot, descriptor, cleanup);
                AddShare(shareB, shareRoot, descriptor, cleanup);

                // Clean up only mappings this fixture successfully created.
                cleanup.Add(() => {
                    if (connectedA) { Status(WNetCancelConnection2(remoteA, 0, false), "Cancel connection A"); connectedA = false; }
                });
                cleanup.Add(() => {
                    if (connectedB) { Status(WNetCancelConnection2(remoteB, 0, false), "Cancel connection B"); connectedB = false; }
                });
                uint first = Connect(remoteA, userA, passwordA);
                result["first_connection_code"] = first;
                Status(first, "Temporary connection A");
                connectedA = true;
                Require(Equal(File.ReadAllBytes(remoteA + "\\seed.txt"), payload), "SMB fixture read differs.");
                result["smb_read_matches"] = true;
                if (inspect != null) inspect(shareA);

                uint conflict = Connect(remoteB, userB, passwordB);
                if (conflict == 0) connectedB = true;
                result["credential_conflict_code"] = conflict;
                Require(conflict == 1219, "Expected credential conflict 1219; received " + conflict);

                string source = Path.Combine(root, "netonly source");
                Directory.CreateDirectory(source);
                File.WriteAllBytes(Path.Combine(source, "copied.txt"), payload);
                result["netonly_copy"] = NetOnly(userA, passwordA, engine, source, remoteA,
                    Path.Combine(output, "netonly-copy.native.log"), false);
                Require(Equal(File.ReadAllBytes(Path.Combine(shareRoot, "copied.txt")), payload), "Net-only copy bytes differ.");
                result["netonly_copy_matches"] = true;
                result["netonly_stop"] = NetOnly(userA, passwordA, engine, source, remoteA,
                    Path.Combine(output, "netonly-stop.native.log"), true);
                passwordA.Clear();

                uint cancelA = WNetCancelConnection2(remoteA, 0, false);
                result["cancel_a_code"] = cancelA;
                Status(cancelA, "Cancel connection A before reconnect");
                connectedA = false;
                uint second = Connect(remoteB, userB, passwordB);
                result["after_cancel_connection_code"] = second;
                Status(second, "Temporary connection B after cancel");
                connectedB = true;
                passwordB.Clear();
                Require(Equal(File.ReadAllBytes(remoteB + "\\seed.txt"), payload), "SMB reconnect read differs.");
                result["passed"] = true;
            }
            catch (Exception error)
            {
                result["error"] = error.GetType().Name + ": " + error.Message;
            }
            finally
            {
                for (int i = cleanup.Count - 1; i >= 0; i--)
                {
                    try { cleanup[i](); }
                    catch (Exception error) { cleanupErrors.Add(error.Message); }
                }
                try { if (passwordA != null) passwordA.Dispose(); }
                catch (Exception error) { cleanupErrors.Add(error.Message); }
                try { if (passwordB != null) passwordB.Dispose(); }
                catch (Exception error) { cleanupErrors.Add(error.Message); }
                if (descriptor != IntPtr.Zero) LocalFree(descriptor);
                result["password_buffers_zeroed"] = passwordA != null && passwordA.Zeroed && passwordB != null && passwordB.Zeroed;
                result["connections_removed"] = !connectedA && !connectedB;
                result["cleanup_errors"] = cleanupErrors.ToArray();
                if (cleanupErrors.Count != 0 || connectedA || connectedB || !(bool)result["password_buffers_zeroed"])
                    result["passed"] = false;
            }
            return result;
        }
    }
}
