# doc: 08_language-basics/0550-source-comments.md —— { } 块注释可以跨行，注释里的关键字不是代码
FUNCTION f_brace()
    { IF l_a THEN
      DISPLAY "x"
      END IF }
    IF l_b THEN
        DISPLAY "y"
    END IF
END FUNCTION
