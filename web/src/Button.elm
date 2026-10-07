module Button exposing (Config, action, applyEdits, back, button, cancelDialog, confirmRegeneration, copiedTranscript, copyTranscript, dangerAction, dangerButton, deleteSection, deleteSermon, disclosure, disclosureWithContent, downloadAudio, editAudio, icon, keepSection, labeled, nextMatch, openSermon, previousMatch, primaryAction, primaryButton, regenerate, spinner, textAction, titleInfo, uploadSermon, view, warning)

import Html exposing (Html, details, div, p, strong, text)
import Html.Attributes exposing (attribute, class, title, type_)
import Ui


{-| Neutral text-button styling, also usable on a link or label.
-}
button : Html.Attribute msg
button =
    class "button"


primaryButton : Html.Attribute msg
primaryButton =
    class "button button--primary"


dangerButton : Html.Attribute msg
dangerButton =
    class "button button--danger"


{-| Decorative icon; its button supplies the accessible name.
-}
icon : String -> Html msg
icon iconName =
    Html.node "iconify-icon"
        [ class "button__icon"
        , attribute "icon" iconName
        , attribute "noobserver" ""
        , attribute "aria-hidden" "true"
        ]
        []


spinner : Html msg
spinner =
    Html.span [ class "spinner", attribute "aria-hidden" "true" ] [ icon "ph:spinner-gap" ]


type alias Config =
    { icon : String, label : String, backgroundColor : String }


back : Config
back =
    Config "ph:arrow-left" "Back to Sermons" "var(--ink-soft)"


cancelDialog : Config
cancelDialog =
    Config "ph:x" "Cancel" "var(--ink-soft)"


confirmRegeneration : Config
confirmRegeneration =
    Config "ph:arrow-clockwise" "Confirm regeneration" "var(--green)"


openSermon : Config
openSermon =
    Config "ph:caret-right" "Open Sermon" "var(--ink-soft)"


uploadSermon : Config
uploadSermon =
    Config "ph:upload-simple" "Upload a Sermon Recording" "var(--green)"


deleteSermon : Config
deleteSermon =
    Config "ph:trash" "Delete Sermon" "var(--red)"


deleteSection : Config
deleteSection =
    Config "ph:trash" "Delete section" "var(--red)"


keepSection : Config
keepSection =
    Config "ph:check" "Keep section" "var(--green)"


downloadAudio : Config
downloadAudio =
    Config "ph:download-simple" "Download audio" "var(--ink-soft)"


editAudio : Config
editAudio =
    Config "ph:scissors" "Edit recording" "var(--green)"


applyEdits : Config
applyEdits =
    Config "ph:check" "Apply edits" "var(--green)"


copyTranscript : Config
copyTranscript =
    Config "ph:copy" "Copy Full Transcript" "var(--ink-soft)"


copiedTranscript : Config
copiedTranscript =
    { copyTranscript | icon = "ph:check" }


titleInfo : Config
titleInfo =
    Config "ph:info" "Why this title?" "var(--ink-soft)"


warning : Config
warning =
    Config "ph:warning" "Verify AI-generated content" "var(--gold-dark)"


previousMatch : Config
previousMatch =
    Config "ph:caret-left" "Previous match" "var(--ink-soft)"


nextMatch : Config
nextMatch =
    Config "ph:caret-right" "Next match" "var(--ink-soft)"


regenerate : String -> Config
regenerate label =
    Config "ph:arrow-clockwise" label "var(--ink-soft)"


{-| Routine icon action. Navigation, playback, and tools stay neutral.
-}
action : String -> String -> Bool -> List (Html.Attribute msg) -> Html msg
action iconName caption busy attributes =
    view "button" (Config iconName caption "var(--ink-soft)") busy attributes


{-| Primary actions commit or continue the user's work.
-}
primaryAction : String -> String -> Bool -> List (Html.Attribute msg) -> Html msg
primaryAction iconName caption busy attributes =
    view "button" (Config iconName caption "var(--green)") busy attributes


{-| Destructive actions remove content.
-}
dangerAction : String -> String -> Bool -> List (Html.Attribute msg) -> Html msg
dangerAction iconName caption busy attributes =
    view "button" (Config iconName caption "var(--red)") busy attributes


textAction : String -> String -> Bool -> List (Html.Attribute msg) -> Html msg
textAction caption description busy attributes =
    viewContent "button" (Config "" description "var(--ink-soft)") busy (class "icon-control icon-control--text" :: attributes) (text caption)


{-| An icon action with a visible caption, using the same colors and accessible
description as its icon-only variant.
-}
labeled : String -> Config -> Bool -> List (Html.Attribute msg) -> Html msg
labeled caption config busy attributes =
    viewContent "button"
        config
        busy
        (class "icon-control icon-control--labeled" :: attributes)
        (Html.span [ class "button__content" ] [ icon config.icon, text caption ])


disclosure : Config -> String -> Html msg
disclosure config body =
    disclosureWithContent config [ p [ Ui.panelText ] [ text body ] ]


disclosureWithContent : Config -> List (Html msg) -> Html msg
disclosureWithContent config content =
    details [ class "icon-disclosure" ]
        [ view "summary" config False []
        , div [ class "icon-disclosure__content" ]
            [ div [ Ui.panel ]
                (strong [ Ui.panelTitle ] [ text config.label ] :: content)
            ]
        ]


{-| Render an icon action as a button, link, or disclosure summary. The same
definition owns its icon, accessible label, tooltip, color, and busy state.
-}
view : String -> Config -> Bool -> List (Html.Attribute msg) -> Html msg
view tag config busy attributes =
    viewContent tag config busy attributes (icon config.icon)


viewContent : String -> Config -> Bool -> List (Html.Attribute msg) -> Html msg -> Html msg
viewContent tag config busy attributes body =
    Html.node tag
        ([ class "icon-control"
         , title config.label
         , attribute "aria-label" config.label
         , attribute "aria-busy"
            (if busy then
                "true"

             else
                "false"
            )
         , attribute "style" ("--icon-background: " ++ config.backgroundColor)
         ]
            ++ (if tag == "button" then
                    [ type_ "button" ]

                else
                    []
               )
            ++ attributes
        )
        [ if busy then
            spinner

          else
            body
        ]
