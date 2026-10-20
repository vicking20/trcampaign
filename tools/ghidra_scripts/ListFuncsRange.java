import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.*;
import ghidra.program.model.listing.*;
import java.io.*;
public class ListFuncsRange extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        long lo = Long.parseLong(a[1].replace("0x",""), 16), hi = Long.parseLong(a[2].replace("0x",""), 16);
        for (Function f : currentProgram.getFunctionManager().getFunctions(toAddr(lo), true)) {
            long ea = f.getEntryPoint().getOffset();
            if (ea >= hi) break;
            out.println(Long.toHexString(ea) + " " + f.getBody().getNumAddresses());
        }
        out.close();
    }
}
