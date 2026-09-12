# doc: 12_reports/2474-the-report-routine.md —— format-section 的简写形式 FORMAT EVERY ROW（无 control block）
REPORT rep_fmt(cust_num)
    DEFINE cust_num INTEGER
    FORMAT EVERY ROW
        PRINT cust_num
END REPORT
