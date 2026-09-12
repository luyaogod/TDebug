# doc: 13_programming-tools/2566-conditional-compilation.md + 08_language-basics/0552-preprocessor-directives.md —— &ifdef … [&else …] &endif
FUNCTION f_ifdef()
&ifdef DEBUG
    DISPLAY "debug"
&else
    DISPLAY "release"
&endif
END FUNCTION
