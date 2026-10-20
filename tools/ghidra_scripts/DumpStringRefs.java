// Usage: -postScript DumpStringRefs.java <outfile> <maxFuncsPerString> <substr1> <substr2> ...
// For every defined string containing a substring, decompile the functions that reference it.
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.address.*;
import ghidra.program.model.data.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import java.io.*;
import java.util.*;

public class DumpStringRefs extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        int maxF = Integer.parseInt(a[1]);
        DecompInterface di = new DecompInterface();
        di.openProgram(currentProgram);
        Set<Address> done = new HashSet<>();
        for (int i = 2; i < a.length; i++) {
            String needle = a[i];
            out.println("\n########## STRING: " + needle);
            int fcount = 0;
            for (Data d : currentProgram.getListing().getDefinedData(true)) {
                if (!d.hasStringValue()) continue;
                String s = String.valueOf(d.getValue());
                if (!s.contains(needle)) continue;
                out.println("// string @ " + d.getAddress() + " = \"" + s + "\"");
                for (Reference r : getReferencesTo(d.getAddress())) {
                    Function f = getFunctionContaining(r.getFromAddress());
                    if (f == null || done.contains(f.getEntryPoint())) continue;
                    if (fcount++ >= maxF) break;
                    done.add(f.getEntryPoint());
                    DecompileResults res = di.decompileFunction(f, 60, monitor);
                    out.println("\n// ---- function " + f.getName() + " @ " + f.getEntryPoint() + " (ref from " + r.getFromAddress() + ")");
                    if (res.decompileCompleted()) out.println(res.getDecompiledFunction().getC());
                    else out.println("// decompile failed: " + res.getErrorMessage());
                }
            }
        }
        out.close();
    }
}
