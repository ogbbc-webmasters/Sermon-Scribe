module Ui exposing
    ( badge
    , badgeFailed
    , button
    , card
    , cardInfo
    , cardMeta
    , cardName
    , confirmBox
    , confirmBoxButtons
    , confirmBoxQuestion
    , dangerButton
    , emptyState
    , errorText
    , hint
    , primaryButton
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



-- BUTTONS


{-| Neutral button. -}
button : Html.Attribute msg
button =
    class "button"


{-| Prominent call-to-action button (green). -}
primaryButton : Html.Attribute msg
primaryButton =
    class "button button--primary"


{-| Destructive-action button (red). -}
dangerButton : Html.Attribute msg
dangerButton =
    class "button button--danger"



-- BADGES


{-| Status pill, default (green) look. -}
badge : Html.Attribute msg
badge =
    class "badge"


{-| Status pill for failed/error states (red). -}
badgeFailed : Html.Attribute msg
badgeFailed =
    class "badge badge--failed"



-- CARDS


{-| List-item card (currently the sermon card). -}
card : Html.Attribute msg
card =
    class "sermon-card"


cardInfo : Html.Attribute msg
cardInfo =
    class "sermon-card__info"


cardName : Html.Attribute msg
cardName =
    class "sermon-card__name"


cardMeta : Html.Attribute msg
cardMeta =
    class "sermon-card__meta"


sermonActions : Html.Attribute msg
sermonActions =
    class "sermon-actions"



-- CONFIRMATION


{-| Inline confirmation panel for destructive actions. -}
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


{-| Progress bar track. -}
progress : Html.Attribute msg
progress =
    class "progress"


{-| Progress bar fill (width set inline by the caller). -}
progressFill : Html.Attribute msg
progressFill =
    class "progress__fill"



-- TEXT & STATES


{-| Muted helper text. -}
hint : Html.Attribute msg
hint =
    class "hint"


{-| Error message text. -}
errorText : Html.Attribute msg
errorText =
    class "error-text"


{-| Placeholder for an empty list. -}
emptyState : Html.Attribute msg
emptyState =
    class "empty-state"
