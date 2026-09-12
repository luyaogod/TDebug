# doc: 08_language-basics/0547-string-delimiters.md + 0588-text-literals.md —— 字符串可以跨行，续行里的关键字仍是字符串内容
FUNCTION f_mlstr()
    IF l_a THEN
        DISPLAY "SELECT code
IF x THEN
END IF"
    END IF
END FUNCTION
