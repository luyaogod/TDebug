# doc: 08_language-basics/0682-if.md —— 前后相邻的两个 IF 是兄弟，END IF 只能配最近的 IF
FUNCTION f_siblings()
    IF l_a THEN
        DISPLAY "a"
    END IF
    IF l_b THEN
        DISPLAY "b"
    END IF
END FUNCTION
