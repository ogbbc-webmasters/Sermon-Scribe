module Dialog exposing (view)

import Button
import Html exposing (Html, div, h2, text)
import Html.Attributes exposing (attribute, id)
import Html.Events exposing (onClick, preventDefaultOn)
import Json.Decode as Decode
import Ui


view : { id : String, title : String, onClose : msg } -> List (Html.Attribute msg) -> List (Html msg) -> List (Html msg) -> Html msg
view config attributes content actions =
    Html.node "app-dialog" []
        [ Html.node "dialog"
            ([ Ui.dialog
             , attribute "aria-labelledby" (config.id ++ "-title")
             , attribute "aria-describedby" (config.id ++ "-body")
             , preventDefaultOn "cancel" (Decode.succeed ( config.onClose, True ))
             ]
                ++ attributes
            )
            [ div [ Ui.dialogHeader ]
                [ h2 [ Ui.panelHeading, id (config.id ++ "-title") ] [ text config.title ]
                , Button.action "ph:x" "Close dialog" False [ onClick config.onClose ]
                ]
            , div [ Ui.dialogBody, id (config.id ++ "-body") ] content
            , div [ Ui.dialogActions ] actions
            ]
        ]
