# doc: 08_language-basics/0685-while.md —— WHILE condition … END WHILE
FUNCTION f_while()
    DEFINE l_cnt INTEGER
    WHILE l_cnt <= 10
        LET l_cnt = l_cnt + 1
    END WHILE
END FUNCTION
