# doc: 08_language-basics/0547-string-delimiters.md + 0548-escape-symbol.md —— 双引号字符串里的 \" 是字面引号，不得终止字符串
FUNCTION s_quote()
    DISPLAY "say \"hi"
    INPUT BY NAME g_cust
    END INPUT
END FUNCTION
