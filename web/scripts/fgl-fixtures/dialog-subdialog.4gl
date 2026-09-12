# doc: 11_user-interface/2078-syntax-of-the-procedural-dialog-instruction.md + 2087-the-subdialog-clause.md —— SUBDIALOG [module.]name[(params)] —— 单行、无终止符；语法上与 sub-dialog block 并列
DIALOG sub_a(a_num INTEGER)
    INPUT BY NAME a_num
    END INPUT
END DIALOG

FUNCTION parent_dlg()
    DIALOG
        SUBDIALOG sub_a(a_num)
    END DIALOG
END FUNCTION
