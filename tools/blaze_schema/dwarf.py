# Extract Blaze classes (full names, members with offset/type) from PS3 DWARF.
import pickle,json,sys
from elftools.elf.elffile import ELFFile
e=ELFFile(open(__import__("common").PS3_ELF,'rb'))
d=e.get_dwarf_info()
cus=pickle.load(open(__import__("common").out("blazecus.pkl"),'rb'))
offs=set(o for o,_ in cus)
classes={}
enums={}
def nm(die):
    a=die.attributes.get('DW_AT_name'); return a.value.decode(errors='replace') if a else None
def tname(die,depth=0):
    if die is None: return 'void'
    t=die.tag; n=nm(die)
    if t in('DW_TAG_base_type','DW_TAG_structure_type','DW_TAG_class_type','DW_TAG_union_type','DW_TAG_enumeration_type','DW_TAG_typedef'):
        return fullname(die) if t!='DW_TAG_base_type' else n
    if depth>6: return '?'
    sub=die.get_DIE_from_attribute('DW_AT_type') if 'DW_AT_type' in die.attributes else None
    if t=='DW_TAG_pointer_type': return tname(sub,depth+1)+'*'
    if t=='DW_TAG_reference_type': return tname(sub,depth+1)+'&'
    if t=='DW_TAG_const_type': return 'const '+tname(sub,depth+1)
    if t=='DW_TAG_volatile_type': return tname(sub,depth+1)
    if t=='DW_TAG_array_type': return tname(sub,depth+1)+'[]'
    return t
def resolve(die,depth=0):
    while die is not None and die.tag in('DW_TAG_typedef','DW_TAG_const_type','DW_TAG_volatile_type') and 'DW_AT_type' in die.attributes and depth<10:
        die=die.get_DIE_from_attribute('DW_AT_type'); depth+=1
    return die
def fullname(die):
    parts=[]; x=die
    while x is not None and x.tag!='DW_TAG_compile_unit':
        n=nm(x)
        if n: parts.append(n)
        x=x.get_parent()
    return '::'.join(reversed(parts))
for cu in d.iter_CUs():
    if cu.cu_offset not in offs: continue
    for die in cu.iter_DIEs():
        if die.tag in('DW_TAG_structure_type','DW_TAG_class_type') and 'DW_AT_declaration' not in die.attributes:
            fn=fullname(die)
            if not fn.startswith('Blaze::'): continue
            if fn in classes and classes[fn]['members']: continue
            mem=[];bases=[]
            for ch in die.iter_children():
                if ch.tag=='DW_TAG_member' and 'DW_AT_data_member_location' in ch.attributes:
                    loc=ch.attributes['DW_AT_data_member_location'].value
                    if isinstance(loc,list):
                        v=0;sh=0
                        for b in loc[1:]:
                            v|=(b&0x7f)<<sh; sh+=7
                            if not b&0x80: break
                        loc=v
                    ty=ch.get_DIE_from_attribute('DW_AT_type') if 'DW_AT_type' in ch.attributes else None
                    rt=resolve(ty)
                    mem.append([loc,nm(ch),tname(ty),tname(rt) if rt is not None else None])
                elif ch.tag=='DW_TAG_inheritance':
                    ty=ch.get_DIE_from_attribute('DW_AT_type'); bases.append(tname(ty))
            sz=die.attributes.get('DW_AT_byte_size'); 
            classes[fn]={'size':sz.value if sz else None,'bases':bases,'members':mem}
        elif die.tag=='DW_TAG_enumeration_type':
            fn=fullname(die)
            if not fn.startswith('Blaze::') or fn in enums: continue
            vals=[(nm(c),c.attributes['DW_AT_const_value'].value) for c in die.iter_children() if c.tag=='DW_TAG_enumerator']
            if vals: enums[fn]=vals
json.dump({'classes':classes,'enums':enums},open(__import__("common").out("dwarf.json"),'w'))
print(len(classes),len(enums))
