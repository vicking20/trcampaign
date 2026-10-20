import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.*;
import ghidra.program.model.data.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import java.io.*;
import java.util.*;
public class ListStringFuncs extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        Map<Address,String> seen = new LinkedHashMap<>();
        for (Data d : currentProgram.getListing().getDefinedData(true)) {
            if (!d.hasStringValue()) continue;
            String s = String.valueOf(d.getValue());
            boolean hit = false;
            for (int i = 1; i < a.length; i++) if (s.toLowerCase().contains(a[i].toLowerCase())) hit = true;
            if (!hit) continue;
            for (Reference r : getReferencesTo(d.getAddress())) {
                Function f = getFunctionContaining(r.getFromAddress());
                if (f == null) continue;
                if (!seen.containsKey(f.getEntryPoint())) seen.put(f.getEntryPoint(), s);
            }
        }
        for (Map.Entry<Address,String> e : seen.entrySet())
            out.println(e.getKey() + "\t" + e.getValue().replace("\n"," "));
        out.close();
    }
}
