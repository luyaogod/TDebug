# doc: 08_language-basics/0682-if.md + 0680-for.md —— IF 里套 FOR 里套 IF：三种块各一个栈，不得错位
FUNCTION f_nested()
    IF l_a THEN
        FOR l_i = 1 TO 3
            IF l_b THEN
                DISPLAY "x"
            END IF
        END FOR
    END IF
END FUNCTION
