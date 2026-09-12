# doc: 08_language-basics/0677-case.md —— CASE expression … WHEN … OTHERWISE … END CASE
FUNCTION f_case()
    CASE l_type
        WHEN "A"
            DISPLAY "a"
        OTHERWISE
            DISPLAY "?"
    END CASE
END FUNCTION
