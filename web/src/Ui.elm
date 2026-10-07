module Ui exposing
    ( audioReview
    , audioReviewControls
    , audioReviewLabel
    , audioReviewPlayer
    , badge
    , badgeFailed
    , button
    , buttonWithIcon
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
    , errorPanel
    , hint
    , icon
    , iconButton
    , iconDisclosure
    , iconDisclosureContent
    , panel
    , panelActions
    , panelHeader
    , panelHeading
    , panelText
    , panelTitle
    , primaryButton
    , progress
    , progressFill
    , quietIconButton
    , smallDangerIconButton
    , smallQuietIconButton
    , spinner
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
import Html.Attributes exposing (attribute, class)



-- AUDIO REVIEW


audioReview : Html.Attribute msg
audioReview =
    class "audio-review"


audioReviewPlayer : Html.Attribute msg
audioReviewPlayer =
    class "audio-review__player"


audioReviewLabel : Html.Attribute msg
audioReviewLabel =
    class "audio-review__label"


audioReviewControls : Html.Attribute msg
audioReviewControls =
    class "audio-review__controls"



-- BUTTONS


{-| Neutral button.
-}
button : Html.Attribute msg
button =
    class "button"


buttonWithIcon : Html.Attribute msg
buttonWithIcon =
    class "button button--with-icon"


{-| Prominent call-to-action button (green).
-}
primaryButton : Html.Attribute msg
primaryButton =
    class "button button--primary"


{-| Destructive-action button (red).
-}
dangerButton : Html.Attribute msg
dangerButton =
    class "button button--danger"


{-| Compact circular button whose accessible name is supplied by the caller.
-}
iconButton : Html.Attribute msg
iconButton =
    class "button button--icon"


quietIconButton : Html.Attribute msg
quietIconButton =
    class "button button--icon button--quiet"


smallQuietIconButton : Html.Attribute msg
smallQuietIconButton =
    class "button button--icon button--quiet button--small"


smallDangerIconButton : Html.Attribute msg
smallDangerIconButton =
    class "button button--icon button--quiet-danger button--small"


{-| Decorative icon; its button supplies the accessible name.
-}
icon : String -> Html.Html msg
icon iconName =
    Html.node "iconify-icon"
        [ class "button__icon"
        , attribute "icon" iconName
        , attribute "noobserver" ""
        , attribute "aria-hidden" "true"
        ]
        []


spinner : Html.Html msg
spinner =
    Html.span [ class "spinner", attribute "aria-hidden" "true" ] [ icon "ph:spinner-gap" ]


-- ICON DISCLOSURE


iconDisclosure : Html.Attribute msg
iconDisclosure =
    class "icon-disclosure"


iconDisclosureContent : Html.Attribute msg
iconDisclosureContent =
    class "icon-disclosure__content"


-- PANELS


panel : Html.Attribute msg
panel =
    class "panel"


panelHeader : Html.Attribute msg
panelHeader =
    class "panel__header"


panelHeading : Html.Attribute msg
panelHeading =
    class "panel__heading"


panelActions : Html.Attribute msg
panelActions =
    class "panel__actions"


errorPanel : Html.Attribute msg
errorPanel =
    class "panel panel--error"


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


{-| List-item card (currently the sermon card).
-}
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
