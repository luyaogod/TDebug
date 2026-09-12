# doc: 08_language-basics/0772-methods.md —— 方法形式 FUNCTION (r Type) name() —— 接收者在名字之前
TYPE Rectangle RECORD
    height, width FLOAT
END RECORD

FUNCTION (r Rectangle) area() RETURNS FLOAT
    RETURN r.height * r.width
END FUNCTION
