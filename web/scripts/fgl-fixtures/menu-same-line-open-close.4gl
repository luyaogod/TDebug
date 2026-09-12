# doc: 11_user-interface/1906-syntax-of-the-menu-instruction.md + 08_language-basics/0546-whitespace-separators.md —— 同行开闭：MENU "t" COMMAND "Quit" EXIT MENU END MENU
FUNCTION m_line()
    MENU "Test" COMMAND "Quit" EXIT MENU END MENU
END FUNCTION
