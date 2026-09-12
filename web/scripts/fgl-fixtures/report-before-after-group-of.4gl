# doc: 12_reports/2484-before-after-group-of.md —— FORMAT 段的 BEFORE/AFTER GROUP OF 控制块
REPORT rep_grp(grp_id)
    DEFINE grp_id INTEGER
    FORMAT
        BEFORE GROUP OF grp_id
            PRINT "s"
        AFTER GROUP OF grp_id
            PRINT "e"
END REPORT
