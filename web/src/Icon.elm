module Icon exposing (Config, back, copiedTranscript, copyTranscript, deleteSermon, downloadAudio, editAudio, nextMatch, openSermon, previousMatch, regenerate, titleInfo, view, warning)

import Html exposing (Html)
import Html.Attributes exposing (attribute, title, type_)
import Ui


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


{-| Render an icon action as a button, link, or disclosure summary. The same
definition owns its icon, accessible label, tooltip, color, and busy state.
-}
view : String -> Config -> Bool -> List (Html.Attribute msg) -> Html msg
view tag config busy attributes =
    Html.node tag
        ([ Ui.iconControl
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
            Ui.spinner

          else
            Ui.icon config.icon
        ]
