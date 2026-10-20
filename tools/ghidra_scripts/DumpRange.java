import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.address.*;
import ghidra.program.model.listing.*;
import java.io.*;
public class DumpRange extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        long lo = Long.parseLong(a[1],16), hi = Long.parseLong(a[2],16);
        DecompInterface di = new DecompInterface(); di.openProgram(currentProgram);
        for (Function f : currentProgram.getFunctionManager().getFunctions(toAddr(lo), true)) {
            if (f.getEntryPoint().getOffset() >= hi) break;
            DecompileResults r = di.decompileFunction(f, 30, monitor);
            out.println("\n// ======== " + f.getName() + " @ " + f.getEntryPoint());
            out.println(r.decompileCompleted() ? r.getDecompiledFunction().getC() : "// failed");
        }
        out.close();
    }
}
