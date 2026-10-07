module Ui exposing
    ( badge
    , badgeFailed
    , cardMeta
    , confirmBox
    , confirmBoxButtons
    , confirmBoxQuestion
    , deletedPanel
    , dialog
    , emptyState
    , errorPanel
    , errorText
    , hint
    , interactivePanel
    , panel
    , panelActions
    , panelHeader
    , panelHeading
    , panelSubtitle
    , panelText
    , panelTitle
    , progress
    , progressFill
    , sermonActions
    )

{-| Shared visual vocabulary for Sermon Scribe.

Each helper returns the `Html.Attribute` carrying the CSS class for one
component (or component part) defined in `web/styles.css`. Main.elm (and
future pages) should use these instead of raw class string literals for
anything reusable, so the class names live in exactly one place per side
of the Elm/CSS boundary. Page-specific one-off classes may stay inline.

See docs/styling.md for the naming and state-ownership conventions.

-}

import Html
import Html.Attributes exposing (class)



-- PANELS


panel : Html.Attribute msg
panel =
    class "panel"


deletedPanel : Html.Attribute msg
deletedPanel =
    class "panel panel--deleted"


panelHeader : Html.Attribute msg
panelHeader =
    class "panel__header"


panelHeading : Html.Attribute msg
panelHeading =
    class "panel__heading"


panelSubtitle : Html.Attribute msg
panelSubtitle =
    class "panel__subtitle"


panelActions : Html.Attribute msg
panelActions =
    class "panel__actions"


errorPanel : Html.Attribute msg
errorPanel =
    class "panel panel--error"


interactivePanel : Html.Attribute msg
interactivePanel =
    class "panel panel--interactive"


panelTitle : Html.Attribute msg
panelTitle =
    class "panel__title"


panelText : Html.Attribute msg
panelText =
    class "panel__text"



-- BADGES


{-| Status pill, default (green) look.
-}
badge : Html.Attribute msg
badge =
    class "badge"


{-| Status pill for failed/error states (red).
-}
badgeFailed : Html.Attribute msg
badgeFailed =
    class "badge badge--failed"



-- CARDS


cardMeta : Html.Attribute msg
cardMeta =
    class "sermon-card__meta"


sermonActions : Html.Attribute msg
sermonActions =
    class "sermon-actions"



-- CONFIRMATION


dialog : Html.Attribute msg
dialog =
    class "dialog"


{-| Inline confirmation panel for destructive actions.
-}
confirmBox : Html.Attribute msg
confirmBox =
    class "confirm-box"


confirmBoxQuestion : Html.Attribute msg
confirmBoxQuestion =
    class "confirm-box__question"


confirmBoxButtons : Html.Attribute msg
confirmBoxButtons =
    class "confirm-box__buttons"



-- PROGRESS


{-| Progress bar track.
-}
progress : Html.Attribute msg
progress =
    class "progress"


{-| Progress bar fill (width set inline by the caller).
-}
progressFill : Html.Attribute msg
progressFill =
    class "progress__fill"



-- TEXT & STATES


{-| Muted helper text.
-}
hint : Html.Attribute msg
hint =
    class "hint"


{-| Error message text.
-}
errorText : Html.Attribute msg
errorText =
    class "error-text"


{-| Placeholder for an empty list.
-}
emptyState : Html.Attribute msg
emptyState =
    class "empty-state"
