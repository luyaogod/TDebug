# doc: 08_language-basics/0682-if.md —— 第 2 行是故意不配对的 END IF（截断文件的样子），必须被忽略
FUNCTION f_orphan()
    END IF
    IF l_a THEN
        DISPLAY "a"
    END IF
END FUNCTION
