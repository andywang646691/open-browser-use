set resultText to "not-found"
tell application "System Events"
    if exists process "Google Chrome" then
        tell process "Google Chrome"
            repeat with w in windows
                try
                    repeat with s in sheets of w
                        set sheetName to name of s as text
                        if sheetName is "Allow remote debugging?" or sheetName is "要允许远程调试吗？" then
                            set sheetContents to entire contents of s
                            repeat with nodeRef in sheetContents
                                try
                                    if (role of nodeRef as text) is "AXButton" then
                                        set buttonDescription to description of nodeRef as text
                                        if buttonDescription is "Allow" or buttonDescription is "允许" then
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
