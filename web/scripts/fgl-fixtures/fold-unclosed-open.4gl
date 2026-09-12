# doc: 08_language-basics/0682-if.md + 0680-for.md —— 第 2 行的 IF 故意不闭合：什么也不产出，且不吃掉后面块的配对
FUNCTION f_unclosed()
    IF l_a THEN
        DISPLAY "a"
    FOR l_i = 1 TO 3
        DISPLAY l_i
    END FOR
    IF l_b THEN
        DISPLAY "b"
    END IF
END FUNCTION
