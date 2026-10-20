import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import java.io.*;
public class FuncAt extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        for (int i = 1; i < a.length; i++) {
            Address ad = toAddr(Long.parseLong(a[i].replace("0x",""), 16));
            Function f = getFunctionContaining(ad);
            out.println(ad + " -> " + (f == null ? "none" : f.getName() + " entry " + f.getEntryPoint()));
            if (f != null) for (Reference r : getReferencesTo(f.getEntryPoint())) {
                Function c = getFunctionContaining(r.getFromAddress());
                out.println("     called from " + r.getFromAddress() + " in " + (c == null ? "?" : c.getName() + " @" + c.getEntryPoint()));
            }
        }
        out.close();
    }
}
