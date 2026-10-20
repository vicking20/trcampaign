// Usage: -postScript FindDisp.java <outfile> <hexdisp1> <hexdisp2> ...   lists instructions with a memory operand displacement equal to a value
import ghidra.app.script.GhidraScript;
import ghidra.program.model.listing.*;
import ghidra.program.model.scalar.Scalar;
import java.io.*;
public class FindDisp extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        long[] want = new long[a.length - 1];
        for (int i = 1; i < a.length; i++) want[i - 1] = Long.parseLong(a[i].replace("0x", ""), 16);
        InstructionIterator it = currentProgram.getListing().getInstructions(true);
        while (it.hasNext() && !monitor.isCancelled()) {
            Instruction ins = it.next();
            String s = ins.toString();
            for (int k = 0; k < ins.getNumOperands(); k++) {
                for (Object o : ins.getOpObjects(k)) {
                    if (o instanceof Scalar) {
                        long v = ((Scalar) o).getUnsignedValue();
                        for (long w : want) if (v == w && s.contains("[")) {
                            Function f = getFunctionContaining(ins.getAddress());
                            out.println(ins.getAddress() + "  " + s + "   in " + (f == null ? "?" : f.getName() + "@" + f.getEntryPoint()));
                        }
                    }
                }
            }
        }
        out.close();
    }
}
