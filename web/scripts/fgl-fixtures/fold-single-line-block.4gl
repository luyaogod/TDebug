# doc: 08_language-basics/0546-whitespace-separators.md —— BDL 是自由格式的，整个 IF 可以写在一行
# 单行块折不起来（起止同一行），VS Code 也会忽略，所以第 4 行不产出折叠区。
FUNCTION f_oneline_if()
    DEFINE l_b INTEGER
    IF l_a THEN LET l_b = 1 END IF
    DISPLAY l_b
END FUNCTION
