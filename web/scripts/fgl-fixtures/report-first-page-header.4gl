# doc: 12_reports/2481-first-page-header.md —— FORMAT 段的 [FIRST] PAGE HEADER / PAGE TRAILER / ON LAST ROW 控制块
REPORT rep_fph(cust_num)
    DEFINE cust_num INTEGER
    FORMAT
        FIRST PAGE HEADER
            PRINT "Report"
        PAGE TRAILER
            PRINT "End"
        ON LAST ROW
            PRINT "Last"
END REPORT
