// Usage: -postScript DumpFuncs.java <outfile> <hexaddr1> <hexaddr2> ...
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.address.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import java.io.*;

public class DumpFuncs extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        DecompInterface di = new DecompInterface();
        di.openProgram(currentProgram);
        for (int i = 1; i < a.length; i++) {
            Address ad = toAddr(Long.parseLong(a[i].replace("0x",""), 16));
            Function f = getFunctionContaining(ad);
            if (f == null) { disassemble(ad); f = createFunction(ad, null); }
            if (f == null) { out.println("// no function at " + ad); continue; }
            out.println("\n// ======== " + f.getName() + " @ " + f.getEntryPoint());
            out.println("// callers:");
            for (Reference r : getReferencesTo(f.getEntryPoint()))
                out.println("//   " + r.getFromAddress() + " in " + (getFunctionContaining(r.getFromAddress()) != null ? getFunctionContaining(r.getFromAddress()).getName() : "?"));
            DecompileResults res = di.decompileFunction(f, 60, monitor);
            out.println(res.decompileCompleted() ? res.getDecompiledFunction().getC() : "// failed " + res.getErrorMessage());
        }
        out.close();
    }
}
