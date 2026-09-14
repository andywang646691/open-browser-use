set resultText to "not-found"

on textSafely(theValue)
    try
        return (theValue as text)
    on error
        return ""
    end try
end textSafely

tell application "System Events"
    if exists process "Google Chrome" then
        tell process "Google Chrome"
            repeat with w in windows
                try
                    repeat with s in sheets of w
                        set sheetName to my textSafely(name of s)
                        if sheetName is "Allow remote debugging?" or sheetName is "要允许远程调试吗？" then
                            set sheetContents to entire contents of s
                            repeat with nodeRef in sheetContents
                                try
                                    if (role of nodeRef as text) is "AXButton" then
                                        set buttonDescription to my textSafely(description of nodeRef)
                                        set buttonName to my textSafely(name of nodeRef)
                                        set buttonValue to my textSafely(value of nodeRef)
                                        if buttonDescription is "Allow" or buttonDescription is "允许" or ¬
                                            buttonName is "Allow" or buttonName is "允许" or ¬
                                            buttonValue is "Allow" or buttonValue is "允许" then
                                            perform action "AXPress" of nodeRef
                                            set resultText to "ready"
                                            exit repeat
                                        end if
                                    end if
                                end try
                            end repeat
                        end if
                        if resultText is "ready" then exit repeat
                    end repeat
                end try
                if resultText is "ready" then exit repeat
            end repeat
        end tell
    end if
end tell
return resultText
