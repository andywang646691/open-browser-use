-- Close Chrome's "Chrome is being controlled by automated test software"
-- infobar using macOS Accessibility APIs only.
-- This script does not use screenshots, coordinates, keyboard input,
-- clipboard access, tab navigation, or chrome://inspect.

property chromeProcessName : "Google Chrome"
property maxDepth : 12
property bannerDescriptions : {"信息栏容器", "Info bar container"}
property bannerMarkers : {"自动测试软件正在控制 Chrome", "Chrome is being controlled by automated test software"}
property closeLabels : {"关闭", "关闭按钮", "Close", "Close button", "Dismiss"}

on asTextSafely(theValue)
  try
    return theValue as text
  on error
    return ""
  end try
end asTextSafely

on attributeSafely(theElement, attributeName)
  tell application "System Events"
    tell process chromeProcessName
      try
        if attributeName is "role" then return role of theElement as text
        if attributeName is "name" then return my asTextSafely(name of theElement)
        if attributeName is "description" then return my asTextSafely(description of theElement)
        if attributeName is "value" then return my asTextSafely(value of theElement)
      on error
        return ""
      end try
    end tell
  end tell
  return ""
end attributeSafely

on childrenSafely(theElement)
  tell application "System Events"
    tell process chromeProcessName
      try
        return UI elements of theElement
      on error
        return {}
      end try
    end tell
  end tell
end childrenSafely

on isBanner(theElement)
  set elementDescription to my attributeSafely(theElement, "description")
  set elementName to my attributeSafely(theElement, "name")
  set elementValue to my attributeSafely(theElement, "value")
  return (bannerDescriptions contains elementDescription) or ¬
    (bannerDescriptions contains elementName) or ¬
    (bannerMarkers contains elementDescription) or ¬
    (bannerMarkers contains elementName) or ¬
    (bannerMarkers contains elementValue)
end isBanner

on isCloseButton(theElement)
  if (my attributeSafely(theElement, "role")) is not "AXButton" then return false

  -- The infobar exposes two buttons:
  --   在“设置”中关闭  -> settings action; never press this one
  --   关闭             -> the X/dismiss button; press only this one
  set elementDescription to my attributeSafely(theElement, "description")
  set elementName to my attributeSafely(theElement, "name")
  set elementValue to my attributeSafely(theElement, "value")
  return (closeLabels contains elementDescription) or ¬
    (closeLabels contains elementName) or ¬
    (closeLabels contains elementValue)
end isCloseButton

on pressCloseButtonInTree(theElement, depth)
  if depth > maxDepth then return false

  try
    if my isCloseButton(theElement) then
      tell application "System Events"
        tell process chromeProcessName
          perform action "AXPress" of theElement
        end tell
      end tell
      return true
    end if
  end try

  set childItems to my childrenSafely(theElement)
  repeat with childItem in childItems
    try
      if my pressCloseButtonInTree(contents of childItem, depth + 1) then return true
    end try
  end repeat
  return false
end pressCloseButtonInTree

on closeBannerInTree(theElement, depth)
  if depth > maxDepth then return false

  try
    if my isBanner(theElement) then ¬
      return my pressCloseButtonInTree(theElement, 0)
  end try

  set childItems to my childrenSafely(theElement)
  repeat with childItem in childItems
    try
      if my closeBannerInTree(contents of childItem, depth + 1) then return true
    end try
  end repeat
  return false
end closeBannerInTree

on countBannersInTree(theElement, depth)
  if depth > maxDepth then return 0
  set total to 0

  try
    if my isBanner(theElement) then set total to total + 1
  end try

  set childItems to my childrenSafely(theElement)
  repeat with childItem in childItems
    try
      set total to total + my countBannersInTree(contents of childItem, depth + 1)
    end try
  end repeat
  return total
end countBannersInTree

-- Resolve Chrome's current window through Accessibility; do not activate it.
tell application "System Events"
  if not (exists process chromeProcessName) then return "Chrome is not running"
  tell process chromeProcessName
    if (count of windows) is 0 then return "Chrome has no windows"
    set mainWindow to window 1
  end tell
end tell

set didPressClose to my closeBannerInTree(mainWindow, 0)
delay 0.3
set remainingBanners to my countBannersInTree(mainWindow, 0)

-- The Go helper uses this small machine-readable contract. A successful
-- AXPress is enough to report success; Chrome may need a short moment before
-- the accessibility tree reflects the dismissed infobar.
if didPressClose then
  return "dismissed"
else
  return "not-found"
end if
