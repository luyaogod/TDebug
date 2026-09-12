# doc: 11_user-interface/1990-1994（ON DRAG_START / DRAG_FINISHED / DRAG_ENTER / DRAG_OVER / DROP） —— 拖放事件：ON DRAG_START/FINISHED/ENTER/OVER ( dnd-object ) 与 ON DROP ( dnd-object )
FUNCTION da_c()
    DEFINE dnd ui.DragDrop
    DISPLAY ARRAY arr TO sa.*
        ON DRAG_START (dnd)
            DISPLAY "1"
        ON DRAG_FINISHED (dnd)
            DISPLAY "2"
        ON DRAG_ENTER (dnd)
            DISPLAY "3"
        ON DRAG_OVER (dnd)
            DISPLAY "4"
        ON DROP (dnd)
            DISPLAY "5"
    END DISPLAY
END FUNCTION
