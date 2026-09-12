# doc: 08_language-basics/0772-methods.md —— selectionRange 必须落在方法名上：接收者标识符 r_area 里含子串 area
TYPE Rect RECORD
    w FLOAT
END RECORD

FUNCTION (r_area Rect) area() RETURNS FLOAT
    RETURN r_area.w * r_area.w
END FUNCTION
