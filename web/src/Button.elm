module Button exposing (Config, action, back, button, copiedTranscript, copyTranscript, dangerButton, deleteSermon, disclosure, downloadAudio, editAudio, icon, nextMatch, openSermon, previousMatch, primaryButton, regenerate, spinner, titleInfo, view, warning)

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


openSermon : Config
openSermon =
    Config "ph:caret-right" "Open Sermon" "var(--ink-soft)"


deleteSermon : Config
deleteSermon =
    Config "ph:trash" "Delete Sermon" "var(--red)"


downloadAudio : Config
downloadAudio =
    Config "ph:download-simple" "Download audio" "var(--green)"


editAudio : Config
editAudio =
    Config "ph:scissors" "Edit recording" "var(--green)"


copyTranscript : Config
copyTranscript =
    Config "ph:copy" "Copy Full Transcript" "var(--green)"


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
    Config "ph:arrow-clockwise" label "var(--green)"


{-| Green icon action with a tooltip and accessible name.
-}
action : String -> String -> Bool -> List (Html.Attribute msg) -> Html msg
action iconName caption busy attributes =
    view "button" (Config iconName caption "var(--green)") busy attributes


disclosure : Config -> String -> Html msg
disclosure config body =
    details [ class "icon-disclosure" ]
        [ view "summary" config False []
        , div [ class "icon-disclosure__content" ]
            [ div [ Ui.panel ]
                [ strong [ Ui.panelTitle ] [ text config.label ]
                , p [ Ui.panelText ] [ text body ]
                ]
            ]
        ]


{-| Render an icon action as a button, link, or disclosure summary. The same
definition owns its icon, accessible label, tooltip, color, and busy state.
-}
view : String -> Config -> Bool -> List (Html.Attribute msg) -> Html msg
view tag config busy attributes =
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
            icon config.icon
        ]
